package hiring

import (
	"context"
)

type CreateOfferInput struct {
	BaseAmountMinor int64  `json:"baseAmountMinor"`
	Currency        string `json:"currency"`
}

type JobOffer struct {
	ID                 string  `json:"id"`
	ApplicantID        string  `json:"applicantId"`
	BaseAmountMinor    int64   `json:"baseAmountMinor"`
	Currency           string  `json:"currency"`
	DocumentID         *string `json:"documentId"`
	SignatureRequestID *string `json:"signatureRequestId"`
	Status             string  `json:"status"`
}

func (service *Service) CreateOffer(ctx context.Context, organizationID, actorUserID, applicantID string, input CreateOfferInput) (JobOffer, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return JobOffer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify applicant belongs to a job posting in this organization
	var exists bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM job_applicants a
			JOIN job_postings p ON p.id = a.job_posting_id
			WHERE a.id = $1 AND p.organization_id = $2
		)
	`, applicantID, organizationID).Scan(&exists)
	if err != nil {
		return JobOffer{}, err
	}
	if !exists {
		return JobOffer{}, ErrNotFound
	}

	// Create offer
	var o JobOffer
	err = tx.QueryRow(ctx, `
		INSERT INTO job_offers (job_applicant_id, base_amount_minor, currency, status, created_by_user_id)
		VALUES ($1, $2, $3, 'draft', $4)
		RETURNING id::text, job_applicant_id::text, base_amount_minor, currency, document_id::text, signature_request_id::text, status
	`, applicantID, input.BaseAmountMinor, input.Currency, actorUserID).Scan(
		&o.ID, &o.ApplicantID, &o.BaseAmountMinor, &o.Currency, &o.DocumentID, &o.SignatureRequestID, &o.Status,
	)
	if err != nil {
		return JobOffer{}, err
	}

	// Update applicant status
	_, err = tx.Exec(ctx, `UPDATE job_applicants SET status = 'offered' WHERE id = $1`, applicantID)
	if err != nil {
		return JobOffer{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return JobOffer{}, err
	}
	return o, nil
}
