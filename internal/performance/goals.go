package performance

import (
	"context"
)

type PerformanceGoal struct {
	ID                   string  `json:"id"`
	EmployeeID           string  `json:"employeeId"`
	EmployeeName         string  `json:"employeeName"`
	Department           string  `json:"department"`
	Title                string  `json:"title"`
	Description          *string `json:"description"`
	Status               string  `json:"status"`
	CompletionPercentage int     `json:"completionPercentage"`
	DueDate              *string `json:"dueDate"`
	CreatedAt            string  `json:"createdAt"`
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
		SELECT 
			g.id::text, 
			g.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
			COALESCE(d.name, 'General') as department,
			g.title, 
			g.description, 
			g.status, 
			g.completion_percentage, 
			g.due_date::text,
			g.created_at::text
		FROM performance_goals g
		JOIN employees e ON e.id = g.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE g.organization_id = $1 AND g.employee_id = $2 
		ORDER BY g.created_at DESC
	`, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PerformanceGoal{}
	for rows.Next() {
		var item PerformanceGoal
		if err := rows.Scan(
			&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Department,
			&item.Title, &item.Description, &item.Status, &item.CompletionPercentage,
			&item.DueDate, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) ListAllGoals(ctx context.Context, organizationID string) ([]PerformanceGoal, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			g.id::text, 
			g.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
			COALESCE(d.name, 'General') as department,
			g.title, 
			g.description, 
			g.status, 
			g.completion_percentage, 
			g.due_date::text,
			g.created_at::text
		FROM performance_goals g
		JOIN employees e ON e.id = g.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		WHERE g.organization_id = $1 
		ORDER BY g.created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PerformanceGoal{}
	for rows.Next() {
		var item PerformanceGoal
		if err := rows.Scan(
			&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Department,
			&item.Title, &item.Description, &item.Status, &item.CompletionPercentage,
			&item.DueDate, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateGoal(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateGoalInput) (PerformanceGoal, error) {
	var exists bool
	var firstName, lastName string
	var deptName *string
	err := service.pool.QueryRow(ctx, `
		SELECT 
			true, 
			e.first_name, 
			e.last_name, 
			d.name 
		FROM employees e 
		LEFT JOIN departments d ON d.id = e.department_id 
		WHERE e.id = $1 AND e.organization_id = $2
	`, employeeID, organizationID).Scan(&exists, &firstName, &lastName, &deptName)
	if err != nil || !exists {
		return PerformanceGoal{}, ErrNotFound
	}

	if input.Status == "" {
		input.Status = "not_started"
	}

	var g PerformanceGoal
	g.EmployeeName = firstName + " " + lastName
	if deptName != nil {
		g.Department = *deptName
	} else {
		g.Department = "General"
	}

	err = service.pool.QueryRow(ctx, `
		INSERT INTO performance_goals (organization_id, employee_id, title, description, status, completion_percentage, due_date, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text, employee_id::text, title, description, status, completion_percentage, due_date::text, created_at::text
	`, organizationID, employeeID, input.Title, input.Description, input.Status, input.CompletionPercentage, input.DueDate, actorUserID).Scan(
		&g.ID, &g.EmployeeID, &g.Title, &g.Description, &g.Status, &g.CompletionPercentage, &g.DueDate, &g.CreatedAt,
	)
	if err != nil {
		return PerformanceGoal{}, err
	}

	return g, nil
}
