package operations

import (
	"context"
)

type DataJob struct {
	ID                string  `json:"id"`
	OrganizationID    string  `json:"organizationId"`
	JobType           string  `json:"jobType"`
	Status            string  `json:"status"`
	FileURL           *string `json:"fileUrl"`
	RequestedByUserID string  `json:"requestedByUserId"`
}

type CreateDataJobInput struct {
	JobType string  `json:"jobType"`
	FileURL *string `json:"fileUrl"`
}

func (s *Service) CreateTenantDataJob(ctx context.Context, orgID, userID string, input CreateDataJobInput) (DataJob, error) {
	var j DataJob
	err := s.pool.QueryRow(ctx, `
		INSERT INTO tenant_data_jobs (organization_id, requested_by_user_id, job_type, file_url)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, organization_id::text, job_type, status, file_url, requested_by_user_id::text
	`, orgID, userID, input.JobType, input.FileURL).Scan(
		&j.ID, &j.OrganizationID, &j.JobType, &j.Status, &j.FileURL, &j.RequestedByUserID,
	)
	return j, err
}

func (s *Service) ListPlatformDataJobs(ctx context.Context) ([]DataJob, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, job_type, status, file_url, requested_by_user_id::text FROM tenant_data_jobs ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []DataJob
	for rows.Next() {
		var j DataJob
		if err := rows.Scan(&j.ID, &j.OrganizationID, &j.JobType, &j.Status, &j.FileURL, &j.RequestedByUserID); err != nil {
			return nil, err
		}
		items = append(items, j)
	}
	return items, nil
}
