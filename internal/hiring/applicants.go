package hiring

import (
	"context"
	"fmt"
	"strings"
)

type ApplyInput struct {
	FirstName           string  `json:"firstName"`
	LastName            string  `json:"lastName"`
	Email               string  `json:"email"`
	Phone               *string `json:"phone"`
	CurrentLocation     *string `json:"currentLocation"`
	ExpectedSalaryGross *int64  `json:"expectedSalaryGross"`
	Currency            *string `json:"currency"`
	NoticePeriod        *string `json:"noticePeriod"`
	LinkedInURL         *string `json:"linkedinUrl"`
	PortfolioURL        *string `json:"portfolioUrl"`
	CoverLetter         *string `json:"coverLetter"`
	ResumeURL           *string `json:"resumeUrl"`
	ResumeFilename      *string `json:"resumeFilename"`
	ResumeDocumentID    *string `json:"resumeDocumentId"`
}

type JobApplicant struct {
	ID                  string  `json:"id"`
	JobID               string  `json:"jobId"`
	JobTitle            string  `json:"jobTitle"`
	FirstName           string  `json:"firstName"`
	LastName            string  `json:"lastName"`
	Email               string  `json:"email"`
	Phone               *string `json:"phone"`
	CurrentLocation     *string `json:"currentLocation"`
	ExpectedSalaryGross *int64  `json:"expectedSalaryGross"`
	Currency            *string `json:"currency"`
	NoticePeriod        *string `json:"noticePeriod"`
	LinkedInURL         *string `json:"linkedinUrl"`
	PortfolioURL        *string `json:"portfolioUrl"`
	CoverLetter         *string `json:"coverLetter"`
	ResumeURL           *string `json:"resumeUrl"`
	ResumeFilename      *string `json:"resumeFilename"`
	ResumeDocumentID    *string `json:"resumeDocumentId"`
	Status              string  `json:"status"`
	CreatedAt           string  `json:"createdAt"`
}

// Apply handles public applicant submissions with CV, cover letter, and extended candidate info.
func (service *Service) Apply(ctx context.Context, postingID string, input ApplyInput) (JobApplicant, error) {
	var exists bool
	err := service.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM job_postings WHERE id = $1 AND status = 'published')`,
		postingID,
	).Scan(&exists)
	if err != nil {
		return JobApplicant{}, err
	}
	if !exists {
		return JobApplicant{}, ErrNotFound
	}

	currency := "USD"
	if input.Currency != nil && *input.Currency != "" {
		currency = *input.Currency
	}

	var a JobApplicant
	err = service.pool.QueryRow(ctx, `
		INSERT INTO job_applicants (
			job_posting_id, first_name, last_name, email, phone,
			current_location, expected_salary_gross, currency, notice_period,
			linkedin_url, portfolio_url, cover_letter,
			resume_document_id, resume_url, resume_filename
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING 
			id::text, 
			job_posting_id::text, 
			'' AS job_title,
			first_name, last_name, email, phone,
			current_location, expected_salary_gross, currency, notice_period,
			linkedin_url, portfolio_url, cover_letter,
			resume_document_id::text,
			resume_url,
			resume_filename,
			status,
			to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	`, postingID, input.FirstName, input.LastName, input.Email, input.Phone,
		input.CurrentLocation, input.ExpectedSalaryGross, currency, input.NoticePeriod,
		input.LinkedInURL, input.PortfolioURL, input.CoverLetter,
		input.ResumeDocumentID, input.ResumeURL, input.ResumeFilename).Scan(
		&a.ID, &a.JobID, &a.JobTitle,
		&a.FirstName, &a.LastName, &a.Email, &a.Phone,
		&a.CurrentLocation, &a.ExpectedSalaryGross, &a.Currency, &a.NoticePeriod,
		&a.LinkedInURL, &a.PortfolioURL, &a.CoverLetter,
		&a.ResumeDocumentID, &a.ResumeURL, &a.ResumeFilename,
		&a.Status, &a.CreatedAt,
	)
	if err != nil {
		return JobApplicant{}, err
	}
	return a, nil
}

const applicantSelectFields = `
	ja.id::text, 
	ja.job_posting_id::text, 
	jp.title AS job_title,
	ja.first_name, ja.last_name, ja.email, ja.phone,
	ja.current_location, ja.expected_salary_gross, ja.currency, ja.notice_period,
	ja.linkedin_url, ja.portfolio_url, ja.cover_letter,
	ja.resume_document_id::text,
	ja.resume_url,
	ja.resume_filename,
	ja.status,
	to_char(ja.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')`

// ListApplicants returns all applicants for a given job posting.
func (service *Service) ListApplicants(ctx context.Context, organizationID, postingID string) ([]JobApplicant, error) {
	var orgID string
	if err := service.pool.QueryRow(ctx,
		`SELECT organization_id::text FROM job_postings WHERE id = $1::uuid`,
		postingID,
	).Scan(&orgID); err != nil {
		return nil, ErrNotFound
	}
	if orgID != organizationID {
		return nil, ErrNotFound
	}

	query := fmt.Sprintf(`
		SELECT %s
		FROM job_applicants ja
		JOIN job_postings jp ON jp.id = ja.job_posting_id
		WHERE ja.job_posting_id = $1::uuid
		ORDER BY ja.created_at DESC`, applicantSelectFields)

	rows, err := service.pool.Query(ctx, query, postingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobApplicant{}
	for rows.Next() {
		var a JobApplicant
		if err := rows.Scan(
			&a.ID, &a.JobID, &a.JobTitle,
			&a.FirstName, &a.LastName, &a.Email, &a.Phone,
			&a.CurrentLocation, &a.ExpectedSalaryGross, &a.Currency, &a.NoticePeriod,
			&a.LinkedInURL, &a.PortfolioURL, &a.CoverLetter,
			&a.ResumeDocumentID, &a.ResumeURL, &a.ResumeFilename,
			&a.Status, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// ListAllApplicants returns applicants across all postings for an org.
func (service *Service) ListAllApplicants(ctx context.Context, organizationID string) ([]JobApplicant, error) {
	query := fmt.Sprintf(`
		SELECT %s
		FROM job_applicants ja
		JOIN job_postings jp ON jp.id = ja.job_posting_id
		WHERE jp.organization_id = $1
		ORDER BY ja.created_at DESC`, applicantSelectFields)

	rows, err := service.pool.Query(ctx, query, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []JobApplicant{}
	for rows.Next() {
		var a JobApplicant
		if err := rows.Scan(
			&a.ID, &a.JobID, &a.JobTitle,
			&a.FirstName, &a.LastName, &a.Email, &a.Phone,
			&a.CurrentLocation, &a.ExpectedSalaryGross, &a.Currency, &a.NoticePeriod,
			&a.LinkedInURL, &a.PortfolioURL, &a.CoverLetter,
			&a.ResumeDocumentID, &a.ResumeURL, &a.ResumeFilename,
			&a.Status, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

// UpdateApplicantStatus moves an applicant to a new pipeline stage.
func (service *Service) UpdateApplicantStatus(ctx context.Context, organizationID, applicantID, status string) (JobApplicant, error) {
	validStatuses := map[string]bool{
		"new": true, "screening": true, "interviewing": true,
		"offered": true, "hired": true, "rejected": true,
	}
	if !validStatuses[strings.ToLower(status)] {
		return JobApplicant{}, fmt.Errorf("invalid status: %s", status)
	}

	query := fmt.Sprintf(`
		UPDATE job_applicants ja
		SET status = $1
		FROM job_postings jp
		WHERE ja.id = $2::uuid
		  AND ja.job_posting_id = jp.id
		  AND jp.organization_id = $3
		RETURNING %s`, applicantSelectFields)

	var a JobApplicant
	err := service.pool.QueryRow(ctx, query, strings.ToLower(status), applicantID, organizationID).Scan(
		&a.ID, &a.JobID, &a.JobTitle,
		&a.FirstName, &a.LastName, &a.Email, &a.Phone,
		&a.CurrentLocation, &a.ExpectedSalaryGross, &a.Currency, &a.NoticePeriod,
		&a.LinkedInURL, &a.PortfolioURL, &a.CoverLetter,
		&a.ResumeDocumentID, &a.ResumeURL, &a.ResumeFilename,
		&a.Status, &a.CreatedAt,
	)
	if err != nil {
		return JobApplicant{}, ErrNotFound
	}
	return a, nil
}
