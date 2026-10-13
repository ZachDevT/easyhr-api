package hiring

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type JobPosting struct {
	ID             string  `json:"id"`
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	Location       string  `json:"location"`
	EmploymentType string  `json:"employmentType"`
	Status         string  `json:"status"`
	DepartmentID   *string `json:"departmentId"`
	DepartmentName string  `json:"departmentName"`
	PositionID     *string `json:"positionId"`
	PositionTitle  *string `json:"positionTitle,omitempty"`
	ApplicantCount int     `json:"applicantCount"`
	CreatedAt      string  `json:"createdAt"`
}

type CreateJobPostingInput struct {
	Title          string  `json:"title"`
	Description    string  `json:"description"`
	Location       string  `json:"location"`
	EmploymentType string  `json:"employmentType"`
	Status         string  `json:"status"`
	DepartmentID   *string `json:"departmentId"`
	PositionID     *string `json:"positionId"`
}

func (service *Service) ListPostings(ctx context.Context, organizationID string) ([]JobPosting, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			jp.id::text, 
			jp.title, 
			jp.description, 
			jp.location, 
			jp.employment_type, 
			jp.status, 
			jp.department_id::text,
			COALESCE(d.name, '') AS department_name,
			jp.position_id::text,
			dp.title AS position_title,
			COALESCE(ac.cnt, 0) AS applicant_count,
			to_char(jp.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM job_postings jp
		LEFT JOIN departments d ON d.id = jp.department_id
		LEFT JOIN department_positions dp ON dp.id = jp.position_id
		LEFT JOIN (
			SELECT job_posting_id, COUNT(*) AS cnt
			FROM job_applicants
			GROUP BY job_posting_id
		) ac ON ac.job_posting_id = jp.id
		WHERE jp.organization_id = $1
		ORDER BY jp.created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobPosting{}
	for rows.Next() {
		var p JobPosting
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Description, &p.Location,
			&p.EmploymentType, &p.Status, &p.DepartmentID,
			&p.DepartmentName, &p.PositionID, &p.PositionTitle,
			&p.ApplicantCount, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (service *Service) ListPublicPostings(ctx context.Context, organizationID string) ([]JobPosting, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			jp.id::text, 
			jp.title, 
			jp.description, 
			jp.location, 
			jp.employment_type, 
			jp.status, 
			jp.department_id::text,
			COALESCE(d.name, '') AS department_name,
			jp.position_id::text,
			dp.title AS position_title,
			0 AS applicant_count,
			to_char(jp.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		FROM job_postings jp
		LEFT JOIN departments d ON d.id = jp.department_id
		LEFT JOIN department_positions dp ON dp.id = jp.position_id
		WHERE jp.organization_id = $1 AND jp.status = 'published'
		ORDER BY jp.created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobPosting{}
	for rows.Next() {
		var p JobPosting
		if err := rows.Scan(
			&p.ID, &p.Title, &p.Description, &p.Location,
			&p.EmploymentType, &p.Status, &p.DepartmentID,
			&p.DepartmentName, &p.PositionID, &p.PositionTitle,
			&p.ApplicantCount, &p.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (service *Service) CreatePosting(ctx context.Context, organizationID, actorUserID string, input CreateJobPostingInput) (JobPosting, error) {
	if strings.TrimSpace(input.Title) == "" {
		return JobPosting{}, fmt.Errorf("title is required")
	}
	status := input.Status
	if status == "" {
		status = "draft"
	}
	var p JobPosting
	err := service.pool.QueryRow(ctx, `
		INSERT INTO job_postings (organization_id, title, description, location, employment_type, status, department_id, position_id, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING 
			id::text, title, description, location, employment_type, status, 
			department_id::text,
			'' AS department_name,
			position_id::text,
			NULL AS position_title,
			0 AS applicant_count,
			to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	`, organizationID, strings.TrimSpace(input.Title), input.Description, input.Location,
		input.EmploymentType, status, input.DepartmentID, input.PositionID, actorUserID).Scan(
		&p.ID, &p.Title, &p.Description, &p.Location,
		&p.EmploymentType, &p.Status, &p.DepartmentID,
		&p.DepartmentName, &p.PositionID, &p.PositionTitle,
		&p.ApplicantCount, &p.CreatedAt,
	)
	if err != nil {
		return JobPosting{}, err
	}
	return p, nil
}

func (service *Service) UpdatePostingStatus(ctx context.Context, organizationID, postingID, status string) error {
	validStatuses := map[string]bool{"draft": true, "published": true, "closed": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid status: %s", status)
	}
	result, err := service.pool.Exec(ctx,
		`UPDATE job_postings SET status = $1 WHERE id = $2::uuid AND organization_id = $3`,
		status, postingID, organizationID,
	)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var ErrJobPostingNotFound = errors.New("job posting not found")
