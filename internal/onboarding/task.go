package onboarding

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

var ErrTaskNotFound = errors.New("onboarding task not found")
var ErrTaskForbidden = errors.New("actor is not permitted to update this onboarding task")

type UpdateTaskInput struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

type Task struct {
	ID          string  `json:"id"`
	Title       string  `json:"title"`
	Status      string  `json:"status"`
	DueDate     *string `json:"dueDate"`
	CompletedAt *string `json:"completedAt"`
}

func (input UpdateTaskInput) Validate() error {
	if input.Status != "completed" && input.Status != "open" {
		return fmt.Errorf("status must be completed or open")
	}
	if len(strings.TrimSpace(input.Note)) > 2000 {
		return fmt.Errorf("note must not exceed 2000 characters")
	}
	return nil
}

// UpdateTask permits an employee to complete their own task, a direct manager to
// complete their employee's manager task, and an authorized operational team to
// complete or reopen any tenant task. The instance status is derived from the
// required task state in the same transaction.
func (service *Service) UpdateTask(ctx context.Context, organizationID, actorUserID, taskID string, input UpdateTaskInput, canCompleteAny bool) (Task, error) {
	if err := input.Validate(); err != nil {
		return Task{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var instanceID, employeeID, assigneeType string
	err = tx.QueryRow(ctx, `
		SELECT task.onboarding_instance_id::text, instance.employee_id::text, task.assignee_type
		FROM onboarding_tasks task
		JOIN onboarding_instances instance ON instance.id = task.onboarding_instance_id
		WHERE task.id = $1 AND instance.organization_id = $2
		FOR UPDATE OF task`, taskID, organizationID).Scan(&instanceID, &employeeID, &assigneeType)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrTaskNotFound
	}
	if err != nil {
		return Task{}, err
	}

	isAssignee, err := taskAssignee(ctx, tx, organizationID, employeeID, actorUserID, assigneeType)
	if err != nil {
		return Task{}, err
	}
	if !canCompleteAny && (!isAssignee || input.Status == "open") {
		return Task{}, ErrTaskForbidden
	}

	result := Task{ID: taskID, Status: input.Status}
	if input.Status == "completed" {
		err = tx.QueryRow(ctx, `
			UPDATE onboarding_tasks
			SET status = 'completed', completed_at = now(), completed_by_user_id = $2, completion_note = $3
			WHERE id = $1
			RETURNING title, due_date::text, completed_at::text`, taskID, actorUserID, nullIfBlank(input.Note)).
			Scan(&result.Title, &result.DueDate, &result.CompletedAt)
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE onboarding_tasks
			SET status = 'open', completed_at = NULL, completed_by_user_id = NULL, completion_note = NULL
			WHERE id = $1
			RETURNING title, due_date::text, completed_at::text`, taskID).
			Scan(&result.Title, &result.DueDate, &result.CompletedAt)
	}
	if err != nil {
		return Task{}, err
	}

	if _, err = tx.Exec(ctx, `
		UPDATE onboarding_instances
		SET status = CASE
			WHEN NOT EXISTS (
				SELECT 1 FROM onboarding_tasks
				WHERE onboarding_instance_id = $1 AND is_required AND status NOT IN ('completed', 'skipped')
			) THEN 'completed'
			ELSE 'on_track'
		END,
		completed_at = CASE
			WHEN NOT EXISTS (
				SELECT 1 FROM onboarding_tasks
				WHERE onboarding_instance_id = $1 AND is_required AND status NOT IN ('completed', 'skipped')
			) THEN now()
			ELSE NULL
		END
		WHERE id = $1`, instanceID); err != nil {
		return Task{}, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata)
		VALUES ($1, $2, $3, 'onboarding_task', $4, 'unknown', jsonb_build_object('status', $5::text))`,
		organizationID, actorUserID, "onboarding_task."+input.Status, taskID, input.Status); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return result, nil
}

func taskAssignee(ctx context.Context, tx pgx.Tx, organizationID, employeeID, actorUserID, assigneeType string) (bool, error) {
	if assigneeType == "employee" {
		var matches bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2 AND user_id = $3)`, employeeID, organizationID, actorUserID).Scan(&matches)
		return matches, err
	}
	if assigneeType == "manager" {
		var matches bool
		err := tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM employment_assignments subject
				JOIN employees manager ON manager.id = subject.manager_employee_id
				WHERE subject.employee_id = $1 AND subject.effective_to IS NULL
				AND manager.organization_id = $2 AND manager.user_id = $3
			)`, employeeID, organizationID, actorUserID).Scan(&matches)
		return matches, err
	}
	return false, nil
}
