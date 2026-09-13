package signatures

import (
	"context"
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
	Status     string               `json:"status"`
	Recipients []SignatureRecipient `json:"recipients"`
}

type SignatureRecipient struct {
	ID     string `json:"id"`
	Email  string `json:"email"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (service *Service) CreateRequest(ctx context.Context, organizationID, actorUserID, documentID string, input CreateSignatureRequestInput) (SignatureRequest, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Verify document belongs to org and is available
	var objectKey string
	err = tx.QueryRow(ctx, `SELECT object_key FROM documents WHERE id = $1 AND organization_id = $2 AND status = 'available'`, documentID, organizationID).Scan(&objectKey)
	if err != nil {
		return SignatureRequest{}, ErrNotFound
	}

	// Persist the request
	var reqID string
	err = tx.QueryRow(ctx, `
		INSERT INTO signature_requests (organization_id, document_id, provider, status, custom_message, created_by_user_id)
		VALUES ($1, $2, 'docusign', 'sent', $3, $4)
		RETURNING id::text
	`, organizationID, documentID, input.Message, actorUserID).Scan(&reqID)
	if err != nil {
		return SignatureRequest{}, err
	}

	reqData := EnvelopeRequest{
		DocumentID: documentID,
		Message:    input.Message,
		Recipients: make([]RecipientData, len(input.Recipients)),
	}

	responseRecipients := make([]SignatureRecipient, len(input.Recipients))

	for i, r := range input.Recipients {
		reqData.Recipients[i] = RecipientData{Email: r.Email, Name: r.Name}
		var recID string
		err = tx.QueryRow(ctx, `
			INSERT INTO signature_recipients (signature_request_id, recipient_email, recipient_name, status)
			VALUES ($1, $2, $3, 'sent')
			RETURNING id::text
		`, reqID, r.Email, r.Name).Scan(&recID)
		if err != nil {
			return SignatureRequest{}, err
		}
		responseRecipients[i] = SignatureRecipient{
			ID:     recID,
			Email:  r.Email,
			Name:   r.Name,
			Status: "sent",
		}
	}

	// Dispatch to DocuSign
	envResp, err := service.provider.SendEnvelope(ctx, reqData)
	if err != nil {
		return SignatureRequest{}, err
	}

	// Update with envelope ID
	_, err = tx.Exec(ctx, `UPDATE signature_requests SET provider_envelope_id = $1 WHERE id = $2`, envResp.ProviderEnvelopeID, reqID)
	if err != nil {
		return SignatureRequest{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return SignatureRequest{}, err
	}

	return SignatureRequest{
		ID:         reqID,
		DocumentID: documentID,
		Status:     "sent",
		Recipients: responseRecipients,
	}, nil
}
