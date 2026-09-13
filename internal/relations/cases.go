package relations

import (
	"context"
)

type RelationCase struct {
	ID          string  `json:"id"`
	EmployeeID  string  `json:"employeeId"`
	Category    string  `json:"category"`
	Severity    string  `json:"severity"`
	Status      string  `json:"status"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

type CreateCaseInput struct {
	EmployeeID  string  `json:"employeeId"`
	Category    string  `json:"category"`
	Severity    string  `json:"severity"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
}

func (service *Service) ListCases(ctx context.Context, organizationID string) ([]RelationCase, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, employee_id::text, category, severity, status, title, description 
		FROM relation_cases 
		WHERE organization_id = $1 
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []RelationCase{}
	for rows.Next() {
		var item RelationCase
		if err := rows.Scan(&item.ID, &item.EmployeeID, &item.Category, &item.Severity, &item.Status, &item.Title, &item.Description); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (service *Service) CreateCase(ctx context.Context, organizationID, actorUserID string, input CreateCaseInput) (RelationCase, error) {
	// Verify employee belongs to org
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, input.EmployeeID, organizationID).Scan(&exists)
	if err != nil {
		return RelationCase{}, err
	}
	if !exists {
		return RelationCase{}, ErrNotFound
	}

	var rc RelationCase
	err = service.pool.QueryRow(ctx, `
		INSERT INTO relation_cases (organization_id, employee_id, category, severity, status, title, description, created_by_user_id)
		VALUES ($1, $2, $3, $4, 'open', $5, $6, $7)
		RETURNING id::text, employee_id::text, category, severity, status, title, description
	`, organizationID, input.EmployeeID, input.Category, input.Severity, input.Title, input.Description, actorUserID).Scan(
		&rc.ID, &rc.EmployeeID, &rc.Category, &rc.Severity, &rc.Status, &rc.Title, &rc.Description,
	)
	if err != nil {
		return RelationCase{}, err
	}

	return rc, nil
}
