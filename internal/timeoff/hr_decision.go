package timeoff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (service *Service) ApproveHR(ctx context.Context, organizationID, approverUserID, requestID, note string) (DecisionResult, error) {
	if len(note) > 2000 {
		return DecisionResult{}, fmt.Errorf("note must contain at most 2000 characters")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var employeeID, leaveTypeID, startDate, status string
	var requested int
	err = tx.QueryRow(ctx, `SELECT employee_id::text,leave_type_id::text,start_date::text,requested_minutes,status FROM leave_requests WHERE id=$1 AND organization_id=$2 FOR UPDATE`, requestID, organizationID).Scan(&employeeID, &leaveTypeID, &startDate, &requested, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrDecisionNotFound
	}
	if err != nil {
		return DecisionResult{}, err
	}
	if status != "pending_hr" {
		return DecisionResult{}, ErrRequestConflict
	}
	var reservationMinutes int
	err = tx.QueryRow(ctx, `SELECT minutes FROM leave_ledger_entries WHERE source_type='leave_request' AND source_id=$1 AND entry_type='reservation' FOR UPDATE`, requestID).Scan(&reservationMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrRequestConflict
	}
	if err != nil {
		return DecisionResult{}, err
	}
	if reservationMinutes != -requested {
		return DecisionResult{}, ErrRequestConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_request_decisions (leave_request_id,stage,decision,decided_by_user_id,note) VALUES ($1,'hr','approved',$2,$3)`, requestID, approverUserID, nullIfBlank(note)); err != nil {
		return DecisionResult{}, err
	}
	// Releasing the reservation then booking usage keeps the immutable ledger
	// balanced and makes the transition visible in historical reports.
	if _, err = tx.Exec(ctx, `INSERT INTO leave_ledger_entries (organization_id,employee_id,leave_type_id,entry_type,minutes,effective_on,source_type,source_id,note,created_by_user_id) VALUES ($1,$2,$3,'release',$4,$5,'leave_request',$6,'Final approval released reservation',$7)`, organizationID, employeeID, leaveTypeID, requested, startDate, requestID, approverUserID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_ledger_entries (organization_id,employee_id,leave_type_id,entry_type,minutes,effective_on,source_type,source_id,note,created_by_user_id) VALUES ($1,$2,$3,'usage',$4,$5,'leave_request_usage',$6,'Final approved leave usage',$7)`, organizationID, employeeID, leaveTypeID, -requested, startDate, requestID, approverUserID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE leave_requests SET status='approved',updated_at=now() WHERE id=$1`, requestID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_request.hr_approved','leave_request',$3,'unknown',jsonb_build_object('requestedMinutes',$4::int))`, organizationID, approverUserID, requestID, requested); err != nil {
		return DecisionResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{ID: requestID, Status: "approved"}, nil
}
