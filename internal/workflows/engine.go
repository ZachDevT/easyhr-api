package workflows

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// In a real system, you'd have an internal event bus to trigger workflows.
// For now, we provide the StartRun primitive.

func (service *Service) StartRun(ctx context.Context, organizationID, definitionID, resourceType, resourceID, initiatedByUserID string) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify definition exists
	var exists bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workflow_definitions WHERE id = $1 AND organization_id = $2 AND status = 'active')`, definitionID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return ErrNotFound
	}

	// Create run
	var runID string
	err = tx.QueryRow(ctx, `
		INSERT INTO workflow_runs (organization_id, workflow_definition_id, resource_type, resource_id, initiated_by_user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`, organizationID, definitionID, resourceType, resourceID, initiatedByUserID).Scan(&runID)
	if err != nil {
		return err
	}

	// Fetch first step
	var stepID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM workflow_steps 
		WHERE workflow_definition_id = $1 
		ORDER BY step_order ASC LIMIT 1
	`, definitionID).Scan(&stepID)

	if err == nil {
		// Create first task assigned to the initiator for simplicity (a real engine resolves assignee_type)
		_, err = tx.Exec(ctx, `
			INSERT INTO workflow_tasks (run_id, step_id, assignee_user_id, status)
			VALUES ($1, $2, $3, 'pending')
		`, runID, stepID, initiatedByUserID)
		if err != nil {
			return err
		}
	} else if err != pgx.ErrNoRows {
		return err
	} else {
		// No steps, auto-approve
		_, err = tx.Exec(ctx, `UPDATE workflow_runs SET status = 'approved' WHERE id = $1`, runID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
