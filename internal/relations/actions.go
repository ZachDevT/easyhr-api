package relations

import (
	"context"
)

type RelationAction struct {
	ID               string  `json:"id"`
	CaseID           string  `json:"caseId"`
	ActionType       string  `json:"actionType"`
	EffectiveDate    string  `json:"effectiveDate"`
	PaidSuspension   *bool   `json:"paidSuspension"`
	SeveranceDetails *string `json:"severanceDetails"`
	Reason           string  `json:"reason"`
}

type CreateActionInput struct {
	ActionType       string  `json:"actionType"`
	EffectiveDate    string  `json:"effectiveDate"`
	PaidSuspension   *bool   `json:"paidSuspension"`
	SeveranceDetails *string `json:"severanceDetails"`
	Reason           string  `json:"reason"`
}

func (service *Service) ExecuteAction(ctx context.Context, organizationID, actorUserID, caseID string, input CreateActionInput) (RelationAction, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return RelationAction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify case belongs to org and get employeeID
	var employeeID string
	err = tx.QueryRow(ctx, `SELECT employee_id::text FROM relation_cases WHERE id = $1 AND organization_id = $2 FOR UPDATE`, caseID, organizationID).Scan(&employeeID)
	if err != nil {
		return RelationAction{}, ErrNotFound
	}

	// Insert action
	var ra RelationAction
	err = tx.QueryRow(ctx, `
		INSERT INTO relation_actions (case_id, action_type, effective_date, paid_suspension, severance_details, reason, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, case_id::text, action_type, effective_date::text, paid_suspension, severance_details, reason
	`, caseID, input.ActionType, input.EffectiveDate, input.PaidSuspension, input.SeveranceDetails, input.Reason, actorUserID).Scan(
		&ra.ID, &ra.CaseID, &ra.ActionType, &ra.EffectiveDate, &ra.PaidSuspension, &ra.SeveranceDetails, &ra.Reason,
	)
	if err != nil {
		return RelationAction{}, err
	}

	// Determine new lifecycle status based on action
	var targetStatus string
	if input.ActionType == "suspension" {
		targetStatus = "suspended"
	} else if input.ActionType == "termination" {
		targetStatus = "terminated"
	}

	// If this action mandates a lifecycle status change, execute it transactionally
	if targetStatus != "" {
		// Log the status event for the immutable ledger
		_, err = tx.Exec(ctx, `
			INSERT INTO employee_status_events (employee_id, previous_status, new_status, effective_date, reason, recorded_by_user_id)
			SELECT $1, status, $2, $3, $4, $5
			FROM employees WHERE id = $1
		`, employeeID, targetStatus, input.EffectiveDate, input.Reason, actorUserID)
		if err != nil {
			return RelationAction{}, err
		}

		// Hard update the employee's core status to immediately cut off active flows (like payroll/auth)
		_, err = tx.Exec(ctx, `UPDATE employees SET status = $1 WHERE id = $2`, targetStatus, employeeID)
		if err != nil {
			return RelationAction{}, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RelationAction{}, err
	}

	return ra, nil
}
