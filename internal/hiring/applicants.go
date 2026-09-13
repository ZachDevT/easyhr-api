package hiring

import (
	"context"
)

type ApplyInput struct {
	FirstName        string  `json:"firstName"`
	LastName         string  `json:"lastName"`
	Email            string  `json:"email"`
	Phone            *string `json:"phone"`
	ResumeDocumentID *string `json:"resumeDocumentId"`
}

type JobApplicant struct {
	ID               string  `json:"id"`
	JobID            string  `json:"jobId"`
	FirstName        string  `json:"firstName"`
	LastName         string  `json:"lastName"`
	Email            string  `json:"email"`
	Phone            *string `json:"phone"`
	ResumeDocumentID *string `json:"resumeDocumentId"`
	Status           string  `json:"status"`
}

// Apply handles public applicant submissions.
func (service *Service) Apply(ctx context.Context, postingID string, input ApplyInput) (JobApplicant, error) {
	// First verify the posting exists and is published
	var exists bool
	err := service.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM job_postings WHERE id = $1 AND status = 'published')`, postingID).Scan(&exists)
	if err != nil {
		return JobApplicant{}, err
	}
	if !exists {
		return JobApplicant{}, ErrNotFound
	}

	var a JobApplicant
	err = service.pool.QueryRow(ctx, `
		INSERT INTO job_applicants (job_posting_id, first_name, last_name, email, phone, resume_document_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, job_posting_id::text, first_name, last_name, email, phone, resume_document_id::text, status
	`, postingID, input.FirstName, input.LastName, input.Email, input.Phone, input.ResumeDocumentID).Scan(
		&a.ID, &a.JobID, &a.FirstName, &a.LastName, &a.Email, &a.Phone, &a.ResumeDocumentID, &a.Status,
	)
	if err != nil {
		return JobApplicant{}, err
	}
	return a, nil
}
