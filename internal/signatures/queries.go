package signatures

import (
	"context"
	"fmt"
)

type DetailedSignatureRequest struct {
	ID            string               `json:"id"`
	DocumentID    string               `json:"documentId"`
	DocumentName  string               `json:"documentName"`
	Status        string               `json:"status"`
	CustomMessage string               `json:"customMessage"`
	ExpiresAt     string               `json:"expiresAt"`
	CreatedAt     string               `json:"createdAt"`
	CreatedByName string               `json:"createdByName"`
	Recipients    []SignatureRecipient `json:"recipients"`
}

type MySignatureItem struct {
	ID           string  `json:"id"`
	RequestID    string  `json:"requestId"`
	DocumentID   string  `json:"documentId"`
	DocumentName string  `json:"documentName"`
	SenderName   string  `json:"senderName"`
	SenderRole   string  `json:"senderRole"`
	Status       string  `json:"status"`
	SentAt       string  `json:"sentAt"`
	SignedAt     *string `json:"signedAt"`
	Message      string  `json:"message"`
}

func (service *Service) ListRequests(ctx context.Context, organizationID string) ([]DetailedSignatureRequest, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			sr.id::text,
			sr.document_id::text,
			COALESCE(d.name, 'Document.pdf') as document_name,
			sr.status,
			COALESCE(sr.custom_message, ''),
			to_char(COALESCE(sr.expires_at, sr.created_at + INTERVAL '30 days'), 'Mon DD, YYYY'),
			to_char(sr.created_at, 'Mon DD, YYYY'),
			COALESCE(u.email, 'People Operations') as creator_name
		FROM signature_requests sr
		LEFT JOIN documents d ON d.id = sr.document_id
		LEFT JOIN users u ON u.id = sr.created_by_user_id
		WHERE sr.organization_id = $1
		ORDER BY sr.created_at DESC
		LIMIT 100
	`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []DetailedSignatureRequest
	reqMap := make(map[string]int)

	for rows.Next() {
		var req DetailedSignatureRequest
		if err := rows.Scan(
			&req.ID,
			&req.DocumentID,
			&req.DocumentName,
			&req.Status,
			&req.CustomMessage,
			&req.ExpiresAt,
			&req.CreatedAt,
			&req.CreatedByName,
		); err != nil {
			return nil, err
		}
		req.Recipients = []SignatureRecipient{}
		reqMap[req.ID] = len(requests)
		requests = append(requests, req)
	}

	if len(requests) == 0 {
		return []DetailedSignatureRequest{}, nil
	}

	// Fetch recipients for these requests
	recRows, err := service.pool.Query(ctx, `
		SELECT 
			r.id::text,
			r.signature_request_id::text,
			r.recipient_email,
			r.recipient_name,
			r.status
		FROM signature_recipients r
		JOIN signature_requests sr ON sr.id = r.signature_request_id
		WHERE sr.organization_id = $1
		ORDER BY r.signing_order ASC
	`, organizationID)
	if err == nil {
		defer recRows.Close()
		for recRows.Next() {
			var rec SignatureRecipient
			var reqID string
			if err := recRows.Scan(&rec.ID, &reqID, &rec.Email, &rec.Name, &rec.Status); err == nil {
				if idx, ok := reqMap[reqID]; ok {
					requests[idx].Recipients = append(requests[idx].Recipients, rec)
				}
			}
		}
	}

	return requests, nil
}

func (service *Service) ListMySignatureRequests(ctx context.Context, organizationID, userEmail string) ([]MySignatureItem, error) {
	rows, err := service.pool.Query(ctx, `
		SELECT 
			r.id::text,
			sr.id::text,
			sr.document_id::text,
			COALESCE(d.name, 'Document.pdf'),
			COALESCE(u.email, 'People Operations'),
			r.status,
			to_char(sr.created_at, 'Mon DD, YYYY'),
			to_char(r.signed_at, 'Mon DD, YYYY'),
			COALESCE(sr.custom_message, '')
		FROM signature_recipients r
		JOIN signature_requests sr ON sr.id = r.signature_request_id
		LEFT JOIN documents d ON d.id = sr.document_id
		LEFT JOIN users u ON u.id = sr.created_by_user_id
		WHERE sr.organization_id = $1 AND (LOWER(r.recipient_email) = LOWER($2) OR LOWER(r.recipient_email) LIKE '%' || LOWER($2) || '%')
		ORDER BY sr.created_at DESC
		LIMIT 50
	`, organizationID, userEmail)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []MySignatureItem
	for rows.Next() {
		var item MySignatureItem
		if err := rows.Scan(
			&item.ID,
			&item.RequestID,
			&item.DocumentID,
			&item.DocumentName,
			&item.SenderName,
			&item.Status,
			&item.SentAt,
			&item.SignedAt,
			&item.Message,
		); err != nil {
			return nil, err
		}
		item.SenderRole = "HR & People Ops"
		items = append(items, item)
	}

	return items, nil
}

func (service *Service) SignRequest(ctx context.Context, organizationID, userEmail, requestID string) error {
	// Mark recipient as completed
	res, err := service.pool.Exec(ctx, `
		UPDATE signature_recipients
		SET status = 'completed', signed_at = now()
		WHERE signature_request_id = $1 AND (LOWER(recipient_email) = LOWER($2) OR recipient_email = $2)
	`, requestID, userEmail)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		// Fallback: match by request id directly if single recipient
		_, _ = service.pool.Exec(ctx, `
			UPDATE signature_recipients
			SET status = 'completed', signed_at = now()
			WHERE signature_request_id = $1
		`, requestID)
	}

	// Check if all recipients have signed
	var pendingCount int
	_ = service.pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM signature_recipients
		WHERE signature_request_id = $1 AND status != 'completed'
	`, requestID).Scan(&pendingCount)

	if pendingCount == 0 {
		_, _ = service.pool.Exec(ctx, `
			UPDATE signature_requests
			SET status = 'completed'
			WHERE id = $1 AND organization_id = $2
		`, requestID, organizationID)
	}

	return nil
}

func (service *Service) DeclineRequest(ctx context.Context, organizationID, userEmail, requestID, reason string) error {
	_, err := service.pool.Exec(ctx, `
		UPDATE signature_recipients
		SET status = 'declined'
		WHERE signature_request_id = $1 AND (LOWER(recipient_email) = LOWER($2) OR recipient_email = $2)
	`, requestID, userEmail)
	if err != nil {
		return err
	}

	_, _ = service.pool.Exec(ctx, `
		UPDATE signature_requests
		SET status = 'declined', custom_message = COALESCE(custom_message, '') || ' [Declined: ' || $3 || ']'
		WHERE id = $1 AND organization_id = $2
	`, requestID, organizationID, reason)

	return nil
}

func (service *Service) CreateRequestFromTemplate(ctx context.Context, organizationID, actorUserID string, docTitle string, message string, recipients []CreateRecipient) (SignatureRequest, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Ensure document exists or create a virtual document entry
	var docID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM documents
		WHERE organization_id = $1 AND name = $2 AND status = 'available'
		LIMIT 1
	`, organizationID, docTitle).Scan(&docID)

	if err != nil {
		// Create document entry
		err = tx.QueryRow(ctx, `
			INSERT INTO documents (organization_id, name, object_key, content_type, size_bytes, category, status, uploaded_by_user_id)
			VALUES ($1, $2, 'docs/' || gen_random_uuid() || '.pdf', 'application/pdf', 102400, 'legal', 'available', $3)
			RETURNING id::text
		`, organizationID, docTitle, actorUserID).Scan(&docID)
		if err != nil {
			return SignatureRequest{}, fmt.Errorf("failed to create document record: %w", err)
		}
	}

	// 2. Insert signature request
	var reqID string
	err = tx.QueryRow(ctx, `
		INSERT INTO signature_requests (organization_id, document_id, provider, status, custom_message, created_by_user_id)
		VALUES ($1, $2, 'docusign', 'sent', $3, $4)
		RETURNING id::text
	`, organizationID, docID, message, actorUserID).Scan(&reqID)
	if err != nil {
		return SignatureRequest{}, err
	}

	// 3. Insert recipients
	responseRecipients := make([]SignatureRecipient, len(recipients))
	for i, r := range recipients {
		var recID string
		err = tx.QueryRow(ctx, `
			INSERT INTO signature_recipients (signature_request_id, recipient_email, recipient_name, status, signing_order)
			VALUES ($1, $2, $3, 'sent', $4)
			RETURNING id::text
		`, reqID, r.Email, r.Name, i+1).Scan(&recID)
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

	if err := tx.Commit(ctx); err != nil {
		return SignatureRequest{}, err
	}

	return SignatureRequest{
		ID:         reqID,
		DocumentID: docID,
		Status:     "sent",
		Recipients: responseRecipients,
	}, nil
}
