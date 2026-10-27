package signatures

import (
	"context"
	"errors"
	"net/mail"
	"strings"
)

type CreateSignatureRequestInput struct {
	Message    string            `json:"message"`
	Recipients []CreateRecipient `json:"recipients"`
}

type CreateRecipient struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

type SignatureRequest struct {
	ID         string               `json:"id"`
	DocumentID string               `json:"documentId"`
	Title      string               `json:"title,omitempty"`
	Status     string               `json:"status"`
	Recipients []SignatureRecipient `json:"recipients"`
}

type SignatureRecipient struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	Status        string `json:"status"`
	SignatureType string `json:"signatureType,omitempty"`
	SignatureData string `json:"signatureData,omitempty"`
	SignedAt      string `json:"signedAt,omitempty"`
}

func (service *Service) CreateRequest(ctx context.Context, organizationID, actorUserID, documentID string, input CreateSignatureRequestInput) (SignatureRequest, error) {
	if err := validateRecipients(input.Recipients); err != nil {
		return SignatureRequest{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify document belongs to org and is available
	var found bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM documents WHERE id = $1 AND organization_id = $2 AND status = 'available')`, documentID, organizationID).Scan(&found)
	if err != nil || !found {
		return SignatureRequest{}, ErrNotFound
	}

	// Persist the request
	var reqID string
	err = tx.QueryRow(ctx, `
		INSERT INTO signature_requests (organization_id, document_id, provider, status, custom_message, created_by_user_id)
		VALUES ($1, $2, 'native', 'pending', $3, $4)
		RETURNING id::text
	`, organizationID, documentID, input.Message, actorUserID).Scan(&reqID)
	if err != nil {
		return SignatureRequest{}, err
	}

	responseRecipients := make([]SignatureRecipient, len(input.Recipients))

	for i, r := range input.Recipients {
		var recID string
		err = tx.QueryRow(ctx, `
			INSERT INTO signature_recipients (signature_request_id, recipient_email, recipient_name, status)
			VALUES ($1, $2, $3, 'pending')
			RETURNING id::text
		`, reqID, r.Email, r.Name).Scan(&recID)
		if err != nil {
			return SignatureRequest{}, err
		}
		responseRecipients[i] = SignatureRecipient{
			ID:     recID,
			Email:  r.Email,
			Name:   r.Name,
			Status: "pending",
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return SignatureRequest{}, err
	}

	return SignatureRequest{
		ID:         reqID,
		DocumentID: documentID,
		Status:     "pending",
		Recipients: responseRecipients,
	}, nil
}

func validateRecipients(recipients []CreateRecipient) error {
	if len(recipients) == 0 || len(recipients) > 20 {
		return errors.New("between 1 and 20 recipients are required")
	}
	for _, recipient := range recipients {
		address, err := mail.ParseAddress(strings.TrimSpace(recipient.Email))
		if err != nil || address.Address != strings.TrimSpace(recipient.Email) || strings.TrimSpace(recipient.Name) == "" {
			return errors.New("every recipient needs a valid email address and name")
		}
	}
	return nil
}
