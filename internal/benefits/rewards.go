package benefits

import (
	"context"
)

type RewardStatement struct {
	ID          string  `json:"id"`
	EmployeeID  string  `json:"employeeId"`
	PeriodStart string  `json:"periodStart"`
	PeriodEnd   string  `json:"periodEnd"`
	DocumentID  *string `json:"documentId"`
}

type CreateRewardStatementInput struct {
	PeriodStart string  `json:"periodStart"`
	PeriodEnd   string  `json:"periodEnd"`
	DocumentID  *string `json:"documentId"`
}

func (service *Service) CreateRewardStatement(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateRewardStatementInput) (RewardStatement, error) {
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil || !exists {
		return RewardStatement{}, ErrNotFound
	}

	var item RewardStatement
	err = service.pool.QueryRow(ctx, `
		INSERT INTO reward_statements (organization_id, employee_id, period_start, period_end, document_id, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, employee_id::text, period_start::text, period_end::text, document_id::text
	`, organizationID, employeeID, input.PeriodStart, input.PeriodEnd, input.DocumentID, actorUserID).Scan(
		&item.ID, &item.EmployeeID, &item.PeriodStart, &item.PeriodEnd, &item.DocumentID,
	)
	if err != nil {
		return RewardStatement{}, err
	}
	return item, nil
}
