package hiring

import (
	"context"
)

type JobPosting struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	Location       string `json:"location"`
	EmploymentType string `json:"employmentType"`
	Status         string `json:"status"`
}

type CreateJobPostingInput struct {
	Title          string `json:"title"`
	Description    string `json:"description"`
	Location       string `json:"location"`
	EmploymentType string `json:"employmentType"`
	Status         string `json:"status"`
}

func (service *Service) ListPostings(ctx context.Context, organizationID string) ([]JobPosting, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, title, description, location, employment_type, status 
		FROM job_postings 
		WHERE organization_id = $1 
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobPosting{}
	for rows.Next() {
		var p JobPosting
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.Location, &p.EmploymentType, &p.Status); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (service *Service) ListPublicPostings(ctx context.Context, organizationID string) ([]JobPosting, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT id::text, title, description, location, employment_type, status 
		FROM job_postings 
		WHERE organization_id = $1 AND status = 'published'
		ORDER BY created_at DESC
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobPosting{}
	for rows.Next() {
		var p JobPosting
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.Location, &p.EmploymentType, &p.Status); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (service *Service) CreatePosting(ctx context.Context, organizationID, actorUserID string, input CreateJobPostingInput) (JobPosting, error) {
	var p JobPosting
	err := service.pool.QueryRow(ctx, `
		INSERT INTO job_postings (organization_id, title, description, location, employment_type, status, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, title, description, location, employment_type, status
	`, organizationID, input.Title, input.Description, input.Location, input.EmploymentType, input.Status, actorUserID).Scan(
		&p.ID, &p.Title, &p.Description, &p.Location, &p.EmploymentType, &p.Status,
	)
	if err != nil {
		return JobPosting{}, err
	}
	return p, nil
}
