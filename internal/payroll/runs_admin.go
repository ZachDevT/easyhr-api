package payroll

import (
	"context"
	"errors"
)

// ErrConfigurationMissing is returned when a run is requested before the
// organization has saved its payroll country/currency settings.
var ErrConfigurationMissing = errors.New("payroll configuration is missing")

// RunSummary is a pay run joined with its period and aggregated totals so the
// payroll dashboard can render history without N+1 requests.
type RunSummary struct {
	ID              string  `json:"id"`
	PayrollPeriodID string  `json:"payrollPeriodId"`
	Status          string  `json:"status"`
	PeriodStart     string  `json:"periodStart"`
	PeriodEnd       string  `json:"periodEnd"`
	PayDate         string  `json:"payDate"`
	PeriodStatus    string  `json:"periodStatus"`
	Currency        string  `json:"currency"`
	EmployeeCount   int     `json:"employeeCount"`
	TotalBasePay    int64   `json:"totalBasePay"`
	TotalEarnings   int64   `json:"totalEarnings"`
	TotalDeductions int64   `json:"totalDeductions"`
	TotalNetPay     int64   `json:"totalNetPay"`
	PayslipsSent    int     `json:"payslipsSent"`
	CreatedAt       string  `json:"createdAt"`
	ApprovedAt      *string `json:"approvedAt"`
}

func (s *Service) ListRuns(ctx context.Context, organizationID string) ([]RunSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT pr.id::text, pr.payroll_period_id::text, pr.status,
		       pp.start_date::text, pp.end_date::text, pp.pay_date::text, pp.status,
		       COALESCE((SELECT pc.currency FROM payroll_configurations pc WHERE pc.organization_id = pp.organization_id ORDER BY pc.country LIMIT 1), ''),
		       COUNT(pri.id)::int,
		       COALESCE(SUM(pri.base_pay), 0)::bigint,
		       COALESCE(SUM(pri.earnings), 0)::bigint,
		       COALESCE(SUM(pri.deductions), 0)::bigint,
		       COALESCE(SUM(pri.net_pay), 0)::bigint,
		       (SELECT COUNT(*) FROM paystubs ps JOIN payroll_run_items i ON i.id = ps.payroll_run_item_id WHERE i.payroll_run_id = pr.id AND ps.delivery_status = 'delivered')::int,
		       to_char(pr.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(pr.approved_at AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM payroll_runs pr
		JOIN payroll_periods pp ON pp.id = pr.payroll_period_id
		LEFT JOIN payroll_run_items pri ON pri.payroll_run_id = pr.id
		WHERE pp.organization_id = $1
		GROUP BY pr.id, pp.id
		ORDER BY pp.start_date DESC, pr.created_at DESC
		LIMIT 60`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RunSummary, 0)
	for rows.Next() {
		var r RunSummary
		if err := rows.Scan(&r.ID, &r.PayrollPeriodID, &r.Status, &r.PeriodStart, &r.PeriodEnd, &r.PayDate, &r.PeriodStatus, &r.Currency,
			&r.EmployeeCount, &r.TotalBasePay, &r.TotalEarnings, &r.TotalDeductions, &r.TotalNetPay, &r.PayslipsSent, &r.CreatedAt, &r.ApprovedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DiscardRun removes a calculated (not yet approved) run and returns its
// period to draft so payroll can be recalculated after fixing salaries/rules.
func (s *Service) DiscardRun(ctx context.Context, organizationID, runID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var periodID string
	err = tx.QueryRow(ctx, `
		DELETE FROM payroll_runs pr
		USING payroll_periods pp
		WHERE pr.id = $1 AND pp.id = pr.payroll_period_id AND pp.organization_id = $2
		  AND pr.status IN ('draft', 'calculated')
		RETURNING pp.id::text`, runID, organizationID).Scan(&periodID)
	if err != nil {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `
		UPDATE payroll_periods SET status = 'draft'
		WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM payroll_runs WHERE payroll_period_id = $1)`, periodID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) SetTaxRuleActive(ctx context.Context, organizationID, ruleID string, active bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE payroll_tax_rules SET active = $3, updated_at = now() WHERE id = $1 AND organization_id = $2`, ruleID, organizationID, active)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) DeleteTaxRule(ctx context.Context, organizationID, ruleID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM payroll_tax_rules WHERE id = $1 AND organization_id = $2`, ruleID, organizationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
