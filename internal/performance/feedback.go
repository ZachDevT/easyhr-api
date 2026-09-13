package performance

import (
	"context"
)

type FeedbackRecord struct {
	ID             string `json:"id"`
	EmployeeID     string `json:"employeeId"`
	ProviderUserID string `json:"providerUserId"`
	Content        string `json:"content"`
	Visibility     string `json:"visibility"`
}

type CreateFeedbackInput struct {
	Content    string `json:"content"`
	Visibility string `json:"visibility"`
}

func (service *Service) CreateFeedback(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateFeedbackInput) (FeedbackRecord, error) {
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil {
		return FeedbackRecord{}, err
	}
	if !exists {
		return FeedbackRecord{}, ErrNotFound
	}

	var f FeedbackRecord
	err = service.pool.QueryRow(ctx, `
		INSERT INTO feedback_records (organization_id, employee_id, provider_user_id, content, visibility)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text, employee_id::text, provider_user_id::text, content, visibility
	`, organizationID, employeeID, actorUserID, input.Content, input.Visibility).Scan(
		&f.ID, &f.EmployeeID, &f.ProviderUserID, &f.Content, &f.Visibility,
	)
	if err != nil {
		return FeedbackRecord{}, err
	}

	return f, nil
}
