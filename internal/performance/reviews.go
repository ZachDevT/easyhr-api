package performance

import (
	"context"
)

type PerformanceReview struct {
	ID             string  `json:"id"`
	EmployeeID     string  `json:"employeeId"`
	ReviewerUserID string  `json:"reviewerUserId"`
	ReviewCycle    string  `json:"reviewCycle"`
	Status         string  `json:"status"`
	OverallScore   *int    `json:"overallScore"`
	Comments       *string `json:"comments"`
}

type CreateReviewInput struct {
	ReviewCycle  string  `json:"reviewCycle"`
	Status       string  `json:"status"`
	OverallScore *int    `json:"overallScore"`
	Comments     *string `json:"comments"`
}

func (service *Service) CreateReview(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateReviewInput) (PerformanceReview, error) {
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, employeeID, organizationID).Scan(&exists)
	if err != nil {
		return PerformanceReview{}, err
	}
	if !exists {
		return PerformanceReview{}, ErrNotFound
	}

	var r PerformanceReview
	err = service.pool.QueryRow(ctx, `
		INSERT INTO performance_reviews (organization_id, employee_id, reviewer_user_id, review_cycle, status, overall_score, comments)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, employee_id::text, reviewer_user_id::text, review_cycle, status, overall_score, comments
	`, organizationID, employeeID, actorUserID, input.ReviewCycle, input.Status, input.OverallScore, input.Comments).Scan(
		&r.ID, &r.EmployeeID, &r.ReviewerUserID, &r.ReviewCycle, &r.Status, &r.OverallScore, &r.Comments,
	)
	if err != nil {
		return PerformanceReview{}, err
	}

	return r, nil
}
