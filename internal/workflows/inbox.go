package workflows

import (
	"context"
)

type WorkflowTask struct {
	ID        string `json:"id"`
	RunID     string `json:"runId"`
	Status    string `json:"status"`
	CreatedAt string `json:"createdAt"`
}

type DecideTaskInput struct {
	Decision string  `json:"decision"`
	Comments *string `json:"comments"`
}

func (service *Service) ListInbox(ctx context.Context, userID string) ([]WorkflowTask, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, run_id::text, status, created_at::text 
		FROM workflow_tasks 
		WHERE assignee_user_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []WorkflowTask{}
	for rows.Next() {
		var item WorkflowTask
		if err := rows.Scan(&item.ID, &item.RunID, &item.Status, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) DecideTask(ctx context.Context, userID, taskID string, input DecideTaskInput) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify task belongs to user and is pending
	var runID string
	err = tx.QueryRow(ctx, `
		SELECT run_id::text FROM workflow_tasks 
		WHERE id = $1 AND assignee_user_id = $2 AND status = 'pending' FOR UPDATE
	`, taskID, userID).Scan(&runID)
	if err != nil {
		return ErrNotFound
	}

	// Complete task
	_, err = tx.Exec(ctx, `
		UPDATE workflow_tasks 
		SET status = 'completed', decision = $1, comments = $2, completed_at = now() 
		WHERE id = $3
	`, input.Decision, input.Comments, taskID)
	if err != nil {
		return err
	}

	// Update run status based on decision
	if input.Decision == "rejected" {
		_, err = tx.Exec(ctx, `UPDATE workflow_runs SET status = 'rejected' WHERE id = $1`, runID)
	} else {
		// In a real engine, check for next steps. For now, we assume it's the final step.
		_, err = tx.Exec(ctx, `UPDATE workflow_runs SET status = 'approved' WHERE id = $1`, runID)
	}
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}
