package timeoff

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrDecisionForbidden = errors.New("actor is not permitted to decide this leave request")

// Decline releases the original reservation in the same transaction as the
// decision. This preserves the ledger audit trail while making the balance
// immediately usable again.
func (service *Service) Decline(ctx context.Context, organizationID, actorUserID, requestID, reason string, canApproveManager, canApproveHR bool) (DecisionResult, error) {
	if reason = strings.TrimSpace(reason); reason == "" || len(reason) > 2000 {
		return DecisionResult{}, fmt.Errorf("reason must contain between 1 and 2000 characters")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var employeeID, leaveTypeID, startDate, status string
	var requested int
	err = tx.QueryRow(ctx, `SELECT employee_id::text, leave_type_id::text, start_date::text, requested_minutes, status FROM leave_requests WHERE id = $1 AND organization_id = $2 FOR UPDATE`, requestID, organizationID).Scan(&employeeID, &leaveTypeID, &startDate, &requested, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrDecisionNotFound
	}
	if err != nil {
		return DecisionResult{}, err
	}

	stage := ""
	switch status {
	case "pending_manager":
		if !canApproveManager {
			return DecisionResult{}, ErrDecisionForbidden
		}
		var isDirectManager bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employment_assignments subject JOIN employees manager ON manager.id = subject.manager_employee_id WHERE subject.employee_id = $1 AND subject.effective_to IS NULL AND manager.organization_id = $2 AND manager.user_id = $3)`, employeeID, organizationID, actorUserID).Scan(&isDirectManager); err != nil {
			return DecisionResult{}, err
		}
		if !isDirectManager {
			return DecisionResult{}, ErrDecisionForbidden
		}
		stage = "manager"
	case "pending_hr":
		if !canApproveHR {
			return DecisionResult{}, ErrDecisionForbidden
		}
		stage = "hr"
	default:
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
	if _, err = tx.Exec(ctx, `INSERT INTO leave_request_decisions (leave_request_id, stage, decision, decided_by_user_id, note) VALUES ($1, $2, 'declined', $3, $4)`, requestID, stage, actorUserID, reason); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_ledger_entries (organization_id, employee_id, leave_type_id, entry_type, minutes, effective_on, source_type, source_id, note, created_by_user_id) VALUES ($1, $2, $3, 'release', $4, $5, 'leave_request_decline', $6, 'Leave request declined: ' || $7, $8)`, organizationID, employeeID, leaveTypeID, requested, startDate, requestID, reason, actorUserID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE leave_requests SET status = 'declined', updated_at = now() WHERE id = $1`, requestID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata) VALUES ($1, $2, 'leave_request.declined', 'leave_request', $3, 'unknown', jsonb_build_object('stage', $4::text))`, organizationID, actorUserID, requestID, stage); err != nil {
		return DecisionResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{ID: requestID, Status: "declined"}, nil
}
