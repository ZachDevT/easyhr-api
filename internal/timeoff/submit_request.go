package timeoff

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrRequestConflict = errors.New("leave request conflicts with current state")
	ErrRequestNotFound = errors.New("leave policy or employee not found")
)

type SubmitRequestInput struct {
	EmployeeID       string `json:"employeeId"`
	LeaveTypeID      string `json:"leaveTypeId"`
	StartDate        string `json:"startDate"`
	EndDate          string `json:"endDate"`
	RequestedMinutes int    `json:"requestedMinutes"`
	Reason           string `json:"reason,omitempty"`
}

type LeaveRequest struct {
	ID               string  `json:"id"`
	EmployeeID       string  `json:"employeeId"`
	LeaveTypeID      string  `json:"leaveTypeId"`
	StartDate        string  `json:"startDate"`
	EndDate          string  `json:"endDate"`
	RequestedMinutes int     `json:"requestedMinutes"`
	Reason           *string `json:"reason"`
	Status           string  `json:"status"`
	SubmittedAt      string  `json:"submittedAt"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (service *Service) Submit(ctx context.Context, organizationID, actorUserID string, input SubmitRequestInput) (LeaveRequest, error) {
	if err := input.validate(); err != nil {
		return LeaveRequest{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return LeaveRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id=$1 AND organization_id=$2 AND status IN ('onboarding','active','leave'))`, input.EmployeeID, organizationID).Scan(&active)
	if err != nil {
		return LeaveRequest{}, err
	}
	if !active {
		return LeaveRequest{}, ErrRequestNotFound
	}
	var allowsNegative bool
	err = tx.QueryRow(ctx, `SELECT p.allows_negative_balance FROM leave_policy_assignments a JOIN leave_policies p ON p.id=a.leave_policy_id JOIN leave_types t ON t.id=p.leave_type_id WHERE a.employee_id=$1 AND p.leave_type_id=$2 AND p.organization_id=$3 AND p.status='active' AND a.effective_from <= $4::date AND (a.effective_to IS NULL OR a.effective_to >= $4::date) ORDER BY a.effective_from DESC LIMIT 1`, input.EmployeeID, input.LeaveTypeID, organizationID, input.StartDate).Scan(&allowsNegative)
	if errors.Is(err, pgx.ErrNoRows) {
		return LeaveRequest{}, ErrRequestNotFound
	}
	if err != nil {
		return LeaveRequest{}, err
	}
	var available, reserved int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(SUM(CASE WHEN entry_type='reservation' THEN 0 ELSE minutes END),0),COALESCE(-SUM(CASE WHEN entry_type='reservation' THEN minutes ELSE 0 END),0) FROM leave_ledger_entries WHERE employee_id=$1 AND leave_type_id=$2 AND organization_id=$3`, input.EmployeeID, input.LeaveTypeID, organizationID).Scan(&available, &reserved)
	if err != nil {
		return LeaveRequest{}, err
	}
	if err := (Balance{AvailableMinutes: available, ReservedMinutes: reserved}).CanReserve(input.RequestedMinutes, allowsNegative); err != nil {
		return LeaveRequest{}, err
	}
	result := LeaveRequest{EmployeeID: input.EmployeeID, LeaveTypeID: input.LeaveTypeID, StartDate: input.StartDate, EndDate: input.EndDate, RequestedMinutes: input.RequestedMinutes, Status: "pending_manager", SubmittedAt: time.Now().UTC().Format(time.RFC3339)}
	err = tx.QueryRow(ctx, `INSERT INTO leave_requests (organization_id,employee_id,leave_type_id,start_date,end_date,requested_minutes,reason,status,submitted_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'pending_manager',now()) RETURNING id::text,submitted_at::text`, organizationID, input.EmployeeID, input.LeaveTypeID, input.StartDate, input.EndDate, input.RequestedMinutes, nullIfBlank(input.Reason)).Scan(&result.ID, &result.SubmittedAt)
	if err != nil {
		return LeaveRequest{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO leave_ledger_entries (organization_id,employee_id,leave_type_id,entry_type,minutes,effective_on,source_type,source_id,note,created_by_user_id) VALUES ($1,$2,$3,'reservation',$4,$5,'leave_request',$6,'Leave request reservation',$7)`, organizationID, input.EmployeeID, input.LeaveTypeID, -input.RequestedMinutes, input.StartDate, result.ID, actorUserID); err != nil {
		return LeaveRequest{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_request.submitted','leave_request',$3,'unknown',jsonb_build_object('requestedMinutes',$4))`, organizationID, actorUserID, result.ID, input.RequestedMinutes); err != nil {
		return LeaveRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return LeaveRequest{}, err
	}
	return result, nil
}

func (input SubmitRequestInput) validate() error {
	if input.EmployeeID == "" || input.LeaveTypeID == "" {
		return fmt.Errorf("employeeId and leaveTypeId are required")
	}
	start, err := time.Parse(time.DateOnly, input.StartDate)
	if err != nil {
		return fmt.Errorf("startDate must be an ISO date")
	}
	end, err := time.Parse(time.DateOnly, input.EndDate)
	if err != nil || end.Before(start) {
		return fmt.Errorf("endDate must be on or after startDate")
	}
	if input.RequestedMinutes < 1 {
		return fmt.Errorf("requestedMinutes must be positive")
	}
	if len(input.Reason) > 2000 {
		return fmt.Errorf("reason must contain at most 2000 characters")
	}
	return nil
}
func nullIfBlank(value string) any {
	if value == "" {
		return nil
	}
	return value
}
