package timeoff

import (
	"context"
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
	result := LeavePolicy{LeaveTypeID: input.LeaveTypeID, Name: input.Name, AccrualFrequency: input.AccrualFrequency, AccrualMinutes: input.AccrualMinutes, CarryoverLimitMinutes: input.CarryoverLimitMinutes, AllowsNegativeBalance: input.AllowsNegativeBalance, EffectiveFrom: input.EffectiveFrom, Status: "active"}
	err := service.pool.QueryRow(ctx, `INSERT INTO leave_policies (organization_id,leave_type_id,name,accrual_frequency,accrual_minutes,carryover_limit_minutes,allows_negative_balance,effective_from,status,created_by_user_id) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'active',$9) RETURNING id::text`, org, result.LeaveTypeID, result.Name, result.AccrualFrequency, result.AccrualMinutes, result.CarryoverLimitMinutes, result.AllowsNegativeBalance, result.EffectiveFrom, actor).Scan(&result.ID)
	if err != nil {
		var pge *pgconn.PgError
		if errors.As(err, &pge) && pge.Code == "23505" {
			return LeavePolicy{}, ErrLeavePolicyConflict
		}
		return LeavePolicy{}, err
	}
	_, err = service.pool.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_policy.created','leave_policy',$3,'unknown',jsonb_build_object('accrualMinutes',$4,'allowsNegativeBalance',$5))`, org, actor, result.ID, result.AccrualMinutes, result.AllowsNegativeBalance)
	if err != nil {
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
	_, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_policy.assigned','leave_policy',$3,'unknown',jsonb_build_object('employeeId',$4))`, org, actor, policyID, input.EmployeeID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
