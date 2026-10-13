package timeoff

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var ErrLeavePolicyConflict = errors.New("leave policy conflicts with an existing policy")

type CreatePolicyInput struct {
	LeaveTypeID           string `json:"leaveTypeId"`
	Name                  string `json:"name"`
	AccrualFrequency      string `json:"accrualFrequency"`
	AccrualMinutes        int    `json:"accrualMinutes"`
	CarryoverLimitMinutes *int   `json:"carryoverLimitMinutes,omitempty"`
	AllowsNegativeBalance bool   `json:"allowsNegativeBalance"`
	EffectiveFrom         string `json:"effectiveFrom"`
}
type LeavePolicy struct {
	ID                    string `json:"id"`
	LeaveTypeID           string `json:"leaveTypeId"`
	Name                  string `json:"name"`
	AccrualFrequency      string `json:"accrualFrequency"`
	AccrualMinutes        int    `json:"accrualMinutes"`
	CarryoverLimitMinutes *int   `json:"carryoverLimitMinutes,omitempty"`
	AllowsNegativeBalance bool   `json:"allowsNegativeBalance"`
	EffectiveFrom         string `json:"effectiveFrom"`
	Status                string `json:"status"`
}

func (input CreatePolicyInput) Validate() error {
	if input.LeaveTypeID == "" {
		return fmt.Errorf("leaveTypeId is required")
	}
	if len(input.Name) < 1 || len(input.Name) > 160 {
		return fmt.Errorf("name must contain between 1 and 160 characters")
	}
	if input.AccrualFrequency != "none" && input.AccrualFrequency != "weekly" && input.AccrualFrequency != "monthly" && input.AccrualFrequency != "annual" {
		return fmt.Errorf("accrualFrequency is invalid")
	}
	if input.AccrualMinutes < 0 {
		return fmt.Errorf("accrualMinutes must be zero or greater")
	}
	if input.AccrualFrequency == "none" && input.AccrualMinutes != 0 {
		return fmt.Errorf("accrualMinutes must be zero when accrualFrequency is none")
	}
	if input.CarryoverLimitMinutes != nil && *input.CarryoverLimitMinutes < 0 {
		return fmt.Errorf("carryoverLimitMinutes must be zero or greater")
	}
	if _, err := time.Parse(time.DateOnly, input.EffectiveFrom); err != nil {
		return fmt.Errorf("effectiveFrom must be an ISO date")
	}
	return nil
}
func (service *Service) CreatePolicy(ctx context.Context, org, actor string, input CreatePolicyInput) (LeavePolicy, error) {
	if err := input.Validate(); err != nil {
		return LeavePolicy{}, err
	}
	var typeExists bool
	if err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leave_types WHERE id=$1 AND organization_id=$2 AND status='active')`, input.LeaveTypeID, org).Scan(&typeExists); err != nil {
		return LeavePolicy{}, err
	}
	if !typeExists {
		return LeavePolicy{}, ErrRequestNotFound
	}

	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return LeavePolicy{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result := LeavePolicy{LeaveTypeID: input.LeaveTypeID, Name: input.Name, AccrualFrequency: input.AccrualFrequency, AccrualMinutes: input.AccrualMinutes, CarryoverLimitMinutes: input.CarryoverLimitMinutes, AllowsNegativeBalance: input.AllowsNegativeBalance, EffectiveFrom: input.EffectiveFrom, Status: "active"}
	err = tx.QueryRow(ctx, `INSERT INTO leave_policies (organization_id,leave_type_id,name,accrual_frequency,accrual_minutes,carryover_limit_minutes,allows_negative_balance,effective_from,status,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9) RETURNING id::text`, org, result.LeaveTypeID, result.Name, result.AccrualFrequency, result.AccrualMinutes, result.CarryoverLimitMinutes, result.AllowsNegativeBalance, result.EffectiveFrom, actor).Scan(&result.ID)
	if err != nil {
		var pge *pgconn.PgError
		if errors.As(err, &pge) && pge.Code == "23505" {
			return LeavePolicy{}, ErrLeavePolicyConflict
		}
		return LeavePolicy{}, err
	}
	auditMeta, _ := json.Marshal(map[string]any{
		"accrualMinutes":        result.AccrualMinutes,
		"allowsNegativeBalance": result.AllowsNegativeBalance,
	})
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_policy.created','leave_policy',$3,'unknown',$4::jsonb)`, org, actor, result.ID, auditMeta)
	if err != nil {
		return LeavePolicy{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return LeavePolicy{}, err
	}
	return result, nil
}

type AssignPolicyInput struct {
	EmployeeID    string `json:"employeeId"`
	EffectiveFrom string `json:"effectiveFrom"`
}

func (service *Service) AssignPolicy(ctx context.Context, org, actor, policyID string, input AssignPolicyInput) error {
	if input.EmployeeID == "" {
		return fmt.Errorf("employeeId is required")
	}
	if _, err := time.Parse(time.DateOnly, input.EffectiveFrom); err != nil {
		return fmt.Errorf("effectiveFrom must be an ISO date")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM leave_policies WHERE id=$1 AND organization_id=$2 AND status='active')`, policyID, org).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrRequestNotFound
	}
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id=$1 AND organization_id=$2)`, input.EmployeeID, org).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrRequestNotFound
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_policy_assignments (employee_id,leave_policy_id,effective_from,created_by_user_id) VALUES ($1,$2,$3,$4)`, input.EmployeeID, policyID, input.EffectiveFrom, actor); err != nil {
		return err
	}
	assignMeta, _ := json.Marshal(map[string]any{"employeeId": input.EmployeeID})
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_policy.assigned','leave_policy',$3,'unknown',$4::jsonb)`, org, actor, policyID, assignMeta)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type PolicyDetail struct {
	ID                    string  `json:"id"`
	LeaveTypeID           string  `json:"leaveTypeId"`
	LeaveTypeCode         string  `json:"leaveTypeCode"`
	LeaveTypeName         string  `json:"leaveTypeName"`
	Name                  string  `json:"name"`
	AccrualFrequency      string  `json:"accrualFrequency"` // "none", "weekly", "monthly", "annual"
	AccrualMinutes        int     `json:"accrualMinutes"`
	AccrualHours          float64 `json:"accrualHours"`
	CarryoverLimitMinutes *int    `json:"carryoverLimitMinutes,omitempty"`
	AllowsNegativeBalance bool    `json:"allowsNegativeBalance"`
	EffectiveFrom         string  `json:"effectiveFrom"`
	Status                string  `json:"status"`
	AssignedEmployees     int     `json:"assignedEmployees"`
}

func (service *Service) ListPolicies(ctx context.Context, organizationID string) ([]PolicyDetail, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			p.id::text, 
			p.leave_type_id::text, 
			t.code, 
			t.name, 
			p.name, 
			p.accrual_frequency, 
			p.accrual_minutes,
			p.carryover_limit_minutes,
			p.allows_negative_balance,
			p.effective_from::text,
			p.status,
			COUNT(DISTINCT a.employee_id)
		FROM leave_policies p
		JOIN leave_types t ON t.id = p.leave_type_id
		LEFT JOIN leave_policy_assignments a ON a.leave_policy_id = p.id AND (a.effective_to IS NULL OR a.effective_to >= CURRENT_DATE)
		WHERE p.organization_id = $1 AND p.status = 'active'
		GROUP BY p.id, p.leave_type_id, t.code, t.name, p.name, p.accrual_frequency, p.accrual_minutes, p.carryover_limit_minutes, p.allows_negative_balance, p.effective_from, p.status
		ORDER BY p.name ASC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PolicyDetail{}
	for rows.Next() {
		var item PolicyDetail
		if err := rows.Scan(
			&item.ID, &item.LeaveTypeID, &item.LeaveTypeCode, &item.LeaveTypeName,
			&item.Name, &item.AccrualFrequency, &item.AccrualMinutes,
			&item.CarryoverLimitMinutes, &item.AllowsNegativeBalance,
			&item.EffectiveFrom, &item.Status, &item.AssignedEmployees,
		); err != nil {
			return nil, err
		}
		item.AccrualHours = float64(item.AccrualMinutes) / 60.0
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) AssignPolicyToAll(ctx context.Context, org, actor, policyID string) error {
	var leaveTypeID string
	var accrualMinutes int
	err := service.pool.QueryRow(ctx, `
		SELECT leave_type_id::text, accrual_minutes
		FROM leave_policies
		WHERE id = $1 AND organization_id = $2
	`, policyID, org).Scan(&leaveTypeID, &accrualMinutes)
	if err != nil {
		return err
	}

	rows, err := service.pool.Query(ctx, `SELECT id::text FROM employees WHERE organization_id = $1 AND status IN ('active', 'onboarding')`, org)
	if err != nil {
		return err
	}
	defer rows.Close()

	var empIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			empIDs = append(empIDs, id)
		}
	}

	for _, empID := range empIDs {
		_, _ = service.pool.Exec(ctx, `
			INSERT INTO leave_policy_assignments (employee_id, leave_policy_id, effective_from, created_by_user_id)
			VALUES ($1, $2, CURRENT_DATE, $3)
			ON CONFLICT (employee_id, leave_policy_id, effective_from) DO NOTHING
		`, empID, policyID, actor)

		if accrualMinutes > 0 {
			_, _ = service.pool.Exec(ctx, `
				INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, note, created_by_user_id)
				VALUES ($1, $2, $3, 'accrual', $4, CURRENT_DATE, 'policy_grant', 'Initial leave balance credit', $5)
				ON CONFLICT DO NOTHING
			`, org, empID, leaveTypeID, accrualMinutes, actor)
		}
	}
	return nil
}

func (service *Service) RunMonthlyAccrual(ctx context.Context, org, actor string) (int, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT DISTINCT a.employee_id::text, p.leave_type_id::text, p.accrual_minutes
		FROM leave_policy_assignments a
		JOIN leave_policies p ON p.id = a.leave_policy_id
		JOIN employees e ON e.id = a.employee_id
		WHERE p.organization_id = $1
		  AND p.status = 'active'
		  AND p.accrual_frequency = 'monthly'
		  AND p.accrual_minutes > 0
		  AND e.status IN ('active', 'onboarding')
		  AND a.effective_from <= CURRENT_DATE
		  AND (a.effective_to IS NULL OR a.effective_to >= CURRENT_DATE)
	`, org)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	credited := 0
	for rows.Next() {
		var empID, leaveTypeID string
		var mins int
		if err := rows.Scan(&empID, &leaveTypeID, &mins); err == nil {
			_, err = service.pool.Exec(ctx, `
				INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, note, created_by_user_id)
				VALUES ($1, $2, $3, 'accrual', $4, CURRENT_DATE, 'monthly_credit', 'Monthly automated PTO accrual', $5)
			`, org, empID, leaveTypeID, mins, actor)
			if err == nil {
				credited++
			}
		}
	}
	return credited, nil
}

type CreateAdjustmentInput struct {
	EmployeeID  string `json:"employeeId"`
	LeaveTypeID string `json:"leaveTypeId"`
	Minutes     int    `json:"minutes"`
	Note        string `json:"note"`
}

func (service *Service) CreateAdjustment(ctx context.Context, org, actor string, input CreateAdjustmentInput) error {
	if input.EmployeeID == "" || input.LeaveTypeID == "" || input.Minutes == 0 {
		return fmt.Errorf("employeeId, leaveTypeId, and non-zero minutes required")
	}
	_, err := service.pool.Exec(ctx, `
		INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, note, created_by_user_id)
		VALUES ($1, $2, $3, 'adjustment', $4, CURRENT_DATE, 'manual_adjustment', $5, $6)
	`, org, input.EmployeeID, input.LeaveTypeID, input.Minutes, input.Note, actor)
	return err
}
