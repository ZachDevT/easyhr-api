package timeoff

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

var ErrCancellationForbidden = errors.New("actor is not permitted to cancel this leave request")

// Cancel allows the employee who submitted a still-pending request to cancel
// it. Final decisions cannot be reversed through this endpoint because they
// require their own auditable adjustment workflow.
func (service *Service) Cancel(ctx context.Context, organizationID, actorUserID, requestID string) (DecisionResult, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var employeeID, leaveTypeID, startDate, status string
	var requested int
	var isOwner bool
	err = tx.QueryRow(ctx, `
		SELECT request.employee_id::text, request.leave_type_id::text, request.start_date::text, request.requested_minutes, request.status,
		       employee.user_id = $3
		FROM leave_requests request
		JOIN employees employee ON employee.id = request.employee_id
		WHERE request.id = $1 AND request.organization_id = $2
		FOR UPDATE OF request`, requestID, organizationID, actorUserID).Scan(&employeeID, &leaveTypeID, &startDate, &requested, &status, &isOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrDecisionNotFound
	}
	if err != nil {
		return DecisionResult{}, err
	}
	if !isOwner {
		return DecisionResult{}, ErrCancellationForbidden
	}
	if status != "pending_manager" && status != "pending_hr" {
		return DecisionResult{}, ErrRequestConflict
	}
	var reservationMinutes int
	err = tx.QueryRow(ctx, `SELECT minutes FROM leave_ledger_entries WHERE source_type = 'leave_request' AND source_id = $1 AND entry_type = 'reservation' FOR UPDATE`, requestID).Scan(&reservationMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrRequestConflict
	}
	if err != nil {
		return DecisionResult{}, err
	}
	if reservationMinutes != -requested {
		return DecisionResult{}, ErrRequestConflict
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, source_id, note, created_by_user_id) VALUES ($1, $2, $3, 'release', $4, $5, 'leave_request_cancel', $6, 'Employee cancelled leave request', $7)`, organizationID, employeeID, leaveTypeID, requested, startDate, requestID, actorUserID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE leave_requests SET status = 'cancelled', cancelled_at = now(), cancelled_by_user_id = $2, updated_at = now() WHERE id = $1`, requestID, actorUserID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata) VALUES ($1, $2, 'leave_request.cancelled', 'leave_request', $3, 'unknown', '{}'::jsonb)`, organizationID, actorUserID, requestID); err != nil {
		return DecisionResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{ID: requestID, Status: "cancelled"}, nil
}
