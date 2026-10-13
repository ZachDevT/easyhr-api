package performance

import (
	"context"
)

type PerformanceReview struct {
	ID             string  `json:"id"`
	EmployeeID     string  `json:"employeeId"`
	EmployeeName   string  `json:"employeeName"`
	Department     string  `json:"department"`
	ReviewerUserID string  `json:"reviewerUserId"`
	ReviewerName   string  `json:"reviewerName"`
	ReviewCycle    string  `json:"reviewCycle"`
	Status         string  `json:"status"`
	OverallScore   *int    `json:"overallScore"`
	Comments       *string `json:"comments"`
	DueDate        string  `json:"dueDate"`
	CreatedAt      string  `json:"createdAt"`
}

type CreateReviewInput struct {
	ReviewCycle  string  `json:"reviewCycle"`
	Status       string  `json:"status"`
	OverallScore *int    `json:"overallScore"`
	Comments     *string `json:"comments"`
}

func (service *Service) ListReviews(ctx context.Context, organizationID string, employeeID *string) ([]PerformanceReview, error) {
	query := `
		SELECT 
			r.id::text, 
			r.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
			COALESCE(d.name, 'General') as department,
			r.reviewer_user_id::text,
			COALESCE(u.name, 'HR Reviewer') as reviewer_name,
			r.review_cycle, 
			r.status, 
			r.overall_score, 
			r.comments,
			COALESCE((r.created_at + interval '14 days')::date::text, '') as due_date,
			r.created_at::text
		FROM performance_reviews r
		JOIN employees e ON e.id = r.employee_id
		LEFT JOIN departments d ON d.id = e.department_id
		LEFT JOIN users u ON u.id = r.reviewer_user_id
		WHERE r.organization_id = $1 AND ($2::uuid IS NULL OR r.employee_id = $2::uuid)
		ORDER BY r.created_at DESC
	`
	rows, err := service.pool.Query(ctx, query, organizationID, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []PerformanceReview{}
	for rows.Next() {
		var r PerformanceReview
		if err := rows.Scan(
			&r.ID, &r.EmployeeID, &r.EmployeeName, &r.Department,
			&r.ReviewerUserID, &r.ReviewerName,
			&r.ReviewCycle, &r.Status, &r.OverallScore, &r.Comments,
			&r.DueDate, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, r)
	}
	return items, nil
}

func (service *Service) CreateReview(ctx context.Context, organizationID, actorUserID, employeeID string, input CreateReviewInput) (PerformanceReview, error) {
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
		return PerformanceReview{}, ErrNotFound
	}

	if input.Status == "" {
		input.Status = "draft"
	}
	if input.ReviewCycle == "" {
		input.ReviewCycle = "Q3 2026"
	}

	var reviewerName string
	_ = service.pool.QueryRow(ctx, `SELECT COALESCE(name, 'HR Reviewer') FROM users WHERE id = $1`, actorUserID).Scan(&reviewerName)
	if reviewerName == "" {
		reviewerName = "HR Reviewer"
	}

	var r PerformanceReview
	r.EmployeeName = firstName + " " + lastName
	if deptName != nil {
		r.Department = *deptName
	} else {
		r.Department = "General"
	}
	r.ReviewerName = reviewerName

	err = service.pool.QueryRow(ctx, `
		INSERT INTO performance_reviews (organization_id, employee_id, reviewer_user_id, review_cycle, status, overall_score, comments)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, employee_id::text, reviewer_user_id::text, review_cycle, status, overall_score, comments, (created_at + interval '14 days')::date::text, created_at::text
	`, organizationID, employeeID, actorUserID, input.ReviewCycle, input.Status, input.OverallScore, input.Comments).Scan(
		&r.ID, &r.EmployeeID, &r.ReviewerUserID, &r.ReviewCycle, &r.Status, &r.OverallScore, &r.Comments, &r.DueDate, &r.CreatedAt,
	)
	if err != nil {
		return PerformanceReview{}, err
	}

	return r, nil
}
