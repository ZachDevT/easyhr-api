package payroll

import (
	"context"
	"fmt"
)

type Run struct {
	ID              string  `json:"id"`
	PayrollPeriodID string  `json:"payrollPeriodId"`
	Status          string  `json:"status"`
	ApprovedAt      *string `json:"approvedAt"`
}

func (service *Service) InitiateRun(ctx context.Context, organizationID, actorUserID, periodID string) (Run, error) {
	// Verify period
	var period Period
	err := service.pool.QueryRow(ctx, `SELECT id::text, start_date::text, end_date::text, pay_date::text, status FROM payroll_periods WHERE id = $1 AND organization_id = $2`, periodID, organizationID).Scan(&period.ID, &period.StartDate, &period.EndDate, &period.PayDate, &period.Status)
	if err != nil {
		return Run{}, ErrNotFound
	}

	if period.Status != "draft" {
		return Run{}, ErrInvalidState
	}

	// Fetch Config
	var config Configuration
	err = service.pool.QueryRow(ctx, `SELECT id::text, country, currency, standard_monthly_minutes,organization_id::text FROM payroll_configurations WHERE organization_id = $1 LIMIT 1`, organizationID).Scan(&config.ID, &config.Country, &config.Currency, &config.StandardMonthlyMinutes, &config.OrganizationID)
	if err != nil {
		return Run{}, ErrConfigurationMissing
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Run{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Mark period as processing
	_, err = tx.Exec(ctx, `UPDATE payroll_periods SET status = 'processing' WHERE id = $1`, periodID)
	if err != nil {
		return Run{}, err
	}

	// Create run
	var r Run
	err = tx.QueryRow(ctx, `
		INSERT INTO payroll_runs (payroll_period_id, status, created_by_user_id)
		VALUES ($1, 'calculated', $2)
		RETURNING id::text, payroll_period_id::text, status
	`, periodID, actorUserID).Scan(&r.ID, &r.PayrollPeriodID, &r.Status)
	if err != nil {
		return Run{}, err
	}

	// Fetch all active employees
	rows, err := tx.Query(ctx, `SELECT id::text FROM employees WHERE organization_id = $1 AND status IN ('active', 'leave', 'onboarding')`, organizationID)
	if err != nil {
		return Run{}, err
	}
	defer rows.Close()

	var employeeIDs []string
	for rows.Next() {
		var eid string
		if err := rows.Scan(&eid); err != nil {
			return Run{}, err
		}
		employeeIDs = append(employeeIDs, eid)
	}
	rows.Close()

	// Instantiate the real country-specific engine
	engine := EngineFactory(service.pool, config.Country)

	// Calculate and insert items
	for _, employeeID := range employeeIDs {
		item, err := engine.Calculate(ctx, config, employeeID, period)
		if err != nil {
			return Run{}, fmt.Errorf("calculate payroll for employee %s: %w", employeeID, err)
		}

		if item.BasePay == 0 && item.Earnings == 0 {
			continue // Nothing to pay
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO payroll_run_items (payroll_run_id, employee_id, base_pay, earnings, deductions, net_pay)
			VALUES ($1, $2, $3, $4, $5, $6)
		`, r.ID, employeeID, item.BasePay, item.Earnings, item.Deductions, item.NetPay)
		if err != nil {
			return Run{}, err
		}
	}

	// Mark period as review
	_, err = tx.Exec(ctx, `UPDATE payroll_periods SET status = 'review' WHERE id = $1`, periodID)
	if err != nil {
		return Run{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Run{}, err
	}

	return r, nil
}

func (service *Service) LockRun(ctx context.Context, organizationID, actorUserID, runID string) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := tx.Exec(ctx, `
		UPDATE payroll_runs 
		SET status = 'approved', approved_at = now(), approved_by_user_id = $1
		WHERE id = $2 AND status IN ('draft', 'calculated') 
		  AND EXISTS (
			SELECT 1 FROM payroll_periods pp WHERE pp.id = payroll_runs.payroll_period_id AND pp.organization_id = $3
		  )
	`, actorUserID, runID, organizationID)

	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrInvalidState
	}

	// Mark period as locked
	_, err = tx.Exec(ctx, `
		UPDATE payroll_periods pp
		SET status = 'locked'
		FROM payroll_runs pr
		WHERE pr.payroll_period_id = pp.id AND pr.id = $1
	`, runID)
	if err != nil {
		return err
	}

	// Generate paystubs for all run items
	_, err = tx.Exec(ctx, `
		INSERT INTO paystubs (payroll_run_item_id, delivery_status)
		SELECT id, 'pending' FROM payroll_run_items WHERE payroll_run_id = $1
		ON CONFLICT (payroll_run_item_id) DO NOTHING
	`, runID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

type RunItem struct {
	ID           string `json:"id"`
	PayrollRunID string `json:"payrollRunId"`
	EmployeeID   string `json:"employeeId"`
	EmployeeName string `json:"employeeName"`
	Department   string `json:"department"`
	AvatarURL    string `json:"avatarUrl"`
	BasePay      int64  `json:"basePay"`
	Earnings     int64  `json:"earnings"`
	Deductions   int64  `json:"deductions"`
	NetPay       int64  `json:"netPay"`
}

func (service *Service) ListRunItems(ctx context.Context, organizationID, runID string) ([]RunItem, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			pri.id::text,
			pri.payroll_run_id::text,
			pri.employee_id::text,
			COALESCE(e.first_name || ' ' || e.last_name, 'Employee') AS employee_name,
			COALESCE(d.name, 'General') AS department,
			COALESCE(p.avatar_url, '') AS avatar_url,
			pri.base_pay,
			pri.earnings,
			pri.deductions,
			pri.net_pay
		FROM payroll_run_items pri
		JOIN payroll_runs pr ON pr.id = pri.payroll_run_id
		JOIN payroll_periods pp ON pp.id = pr.payroll_period_id
		JOIN employees e ON e.id = pri.employee_id
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		LEFT JOIN employment_assignments ea ON ea.employee_id = e.id
		LEFT JOIN departments d ON d.id = ea.department_id
		WHERE pr.id = $1 AND pp.organization_id = $2
		ORDER BY e.first_name ASC
	`, runID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]RunItem, 0)
	for rows.Next() {
		var item RunItem
		if err := rows.Scan(
			&item.ID, &item.PayrollRunID, &item.EmployeeID,
			&item.EmployeeName, &item.Department, &item.AvatarURL,
			&item.BasePay, &item.Earnings, &item.Deductions, &item.NetPay,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) ListRunsForPeriod(ctx context.Context, organizationID, periodID string) ([]Run, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			pr.id::text, 
			pr.payroll_period_id::text, 
			pr.status, 
			to_char(pr.approved_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM payroll_runs pr
		JOIN payroll_periods pp ON pp.id = pr.payroll_period_id
		WHERE pp.id = $1 AND pp.organization_id = $2
		ORDER BY pr.created_at DESC
	`, periodID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]Run, 0)
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.PayrollPeriodID, &r.Status, &r.ApprovedAt); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, nil
}
