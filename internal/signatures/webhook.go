package signatures

import (
	"context"
)

type WebhookEvent struct {
	EnvelopeID      string
	Status          string
	RecipientEvents []RecipientEvent
}

type RecipientEvent struct {
	Email  string
	Status string
}

func (service *Service) ProcessWebhook(ctx context.Context, event WebhookEvent) error {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Update the request
	_, err = tx.Exec(ctx, `
		UPDATE signature_requests 
		SET status = $1 
		WHERE provider_envelope_id = $2 AND provider = 'docusign'
	`, event.Status, event.EnvelopeID)
	if err != nil {
		return err
	}

	// Fetch request ID
	var reqID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM signature_requests WHERE provider_envelope_id = $1`, event.EnvelopeID).Scan(&reqID)
	if err != nil {
		return err
	}

	// Update recipients
	for _, re := range event.RecipientEvents {
		var q string
		if re.Status == "completed" {
			q = `UPDATE signature_recipients SET status = $1, signed_at = now() WHERE signature_request_id = $2 AND recipient_email = $3`
		} else {
			q = `UPDATE signature_recipients SET status = $1 WHERE signature_request_id = $2 AND recipient_email = $3`
		}
		_, err = tx.Exec(ctx, q, re.Status, reqID, re.Email)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
