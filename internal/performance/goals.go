package performance

import (
	"context"
)

type PerformanceGoal struct {
	ID                   string  `json:"id"`
	EmployeeID           string  `json:"employeeId"`
	Title                string  `json:"title"`
	Description          *string `json:"description"`
	Status               string  `json:"status"`
	CompletionPercentage int     `json:"completionPercentage"`
	DueDate              *string `json:"dueDate"`
}

type CreateGoalInput struct {
	Title                string  `json:"title"`
	Description          *string `json:"description"`
	Status               string  `json:"status"`
	CompletionPercentage int     `json:"completionPercentage"`
	DueDate              *string `json:"dueDate"`
}

func (service *Service) ListGoals(ctx context.Context, organizationID, employeeID string) ([]PerformanceGoal, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, employee_id::text, title, description, status, completion_percentage, due_date::text 
		FROM performance_goals 
		WHERE organization_id = $1 AND employee_id = $2 
		ORDER BY created_at DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PerformanceGoal{}
	for rows.Next() {
		var item PerformanceGoal
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.Title, &item.Description, &item.Status, &item.CompletionPercentage, &item.DueDate); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateGoal(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateGoalInput) (PerformanceGoal, error) {
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil {
		return PerformanceGoal{}, err
	}
	if !exists {
		return PerformanceGoal{}, ErrNotFound
	}

	if input.Status == "" {
		input.Status = "not_started"
	}

	var g PerformanceGoal
	err = service.pool.QueryRow(ctx, `
		INSERT INTO performance_goals (organization_id, employee_id, title, description, status, completion_percentage, due_date, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, employee_id::text, title, description, status, completion_percentage, due_date::text
	`, organizationID, employeeID, input.Title, input.Description, input.Status, input.CompletionPercentage, input.DueDate, actorUserID).Scan(
		&g.ID, &g.EmployeeID, &g.Title, &g.Description, &g.Status, &g.CompletionPercentage, &g.DueDate,
	)
	if err != nil {
		return PerformanceGoal{}, err
	}

	return g, nil
}
