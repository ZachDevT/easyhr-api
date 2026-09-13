package timeoff

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var ErrDecisionNotFound = errors.New("leave request not found")

type DecisionResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (service *Service) ApproveManager(ctx context.Context, organizationID, approverUserID, requestID, note string) (DecisionResult, error) {
	if len(note) > 2000 {
		return DecisionResult{}, fmt.Errorf("note must contain at most 2000 characters")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return DecisionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var employeeID, status string
	err = tx.QueryRow(ctx, `SELECT employee_id::text,status FROM leave_requests WHERE id=$1 AND organization_id=$2 FOR UPDATE`, requestID, organizationID).Scan(&employeeID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return DecisionResult{}, ErrDecisionNotFound
	}
	if err != nil {
		return DecisionResult{}, err
	}
	if status != "pending_manager" {
		return DecisionResult{}, ErrRequestConflict
	}
	var isDirectManager bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employment_assignments subject JOIN employees manager ON manager.id=subject.manager_employee_id WHERE subject.employee_id=$1 AND subject.effective_to IS NULL AND manager.organization_id=$2 AND manager.user_id=$3)`, employeeID, organizationID, approverUserID).Scan(&isDirectManager)
	if err != nil {
		return DecisionResult{}, err
	}
	if !isDirectManager {
		return DecisionResult{}, fmt.Errorf("approver is not the employee's direct manager")
	}
	if _, err = tx.Exec(ctx, `INSERT INTO leave_request_decisions (leave_request_id,stage,decision,decided_by_user_id,note) VALUES ($1,'manager','approved',$2,$3)`, requestID, approverUserID, nullIfBlank(note)); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE leave_requests SET status='pending_hr',updated_at=now() WHERE id=$1`, requestID); err != nil {
		return DecisionResult{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,metadata) VALUES ($1,$2,'leave_request.manager_approved','leave_request',$3,'unknown','{}'::jsonb)`, organizationID, approverUserID, requestID); err != nil {
		return DecisionResult{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return DecisionResult{}, err
	}
	return DecisionResult{ID: requestID, Status: "pending_hr"}, nil
}
