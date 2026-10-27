package signatures

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	"github.com/jackc/pgx/v5"
)

type DetailedSignatureRequest struct {
	ID            string               `json:"id"`
	DocumentID    string               `json:"documentId"`
	DocumentName  string               `json:"documentName"`
	DocumentURL   string               `json:"documentUrl,omitempty"`
	Body          string               `json:"body,omitempty"`
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
	DocumentURL  string  `json:"documentUrl,omitempty"`
	Body         string  `json:"body,omitempty"`
	Header       string  `json:"header,omitempty"`
	Footer       string  `json:"footer,omitempty"`
	LogoURL      string  `json:"logoUrl,omitempty"`
	BrandColor   string  `json:"brandColor,omitempty"`
	WebsiteURL   string  `json:"websiteUrl,omitempty"`
	ContactPhone string  `json:"contactPhone,omitempty"`
	ContactEmail string  `json:"contactEmail,omitempty"`
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
			COALESCE(sr.document_id::text, ''),
			COALESCE(sr.title, d.filename, 'Document.pdf') as document_name,
			COALESCE(sr.body_rendered, ''),
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
			&req.Body,
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
			COALESCE(sr.document_id::text, ''),
			COALESCE(sr.title, d.filename, 'Document.pdf'),
			COALESCE(sr.body_rendered, ''),
			COALESCE(sr.header_rendered, ''),
			COALESCE(sr.footer_rendered, ''),
			COALESCE(sr.logo_url, ''),
			COALESCE(sr.brand_color, '#4F46E5'),
			COALESCE(sr.website_url, ''),
			COALESCE(sr.contact_phone, ''),
			COALESCE(sr.contact_email, ''),
			COALESCE(u.email, 'People Operations'),
			r.status,
			to_char(sr.created_at, 'Mon DD, YYYY'),
			to_char(r.signed_at, 'Mon DD, YYYY'),
			COALESCE(sr.custom_message, '')
		FROM signature_recipients r
		JOIN signature_requests sr ON sr.id = r.signature_request_id
		LEFT JOIN documents d ON d.id = sr.document_id
		LEFT JOIN users u ON u.id = sr.created_by_user_id
		WHERE sr.organization_id = $1 AND LOWER(r.recipient_email) = LOWER($2)
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
			&item.Body,
			&item.Header,
			&item.Footer,
			&item.LogoURL,
			&item.BrandColor,
			&item.WebsiteURL,
			&item.ContactPhone,
			&item.ContactEmail,
			&item.SenderName,
			&item.Status,
			&item.SentAt,
			&item.SignedAt,
			&item.Message,
		); err != nil {
			return nil, err
		}
		if item.Status == "completed" {
			item.Status = "signed"
		}
		item.SenderRole = "HR & People Ops"
		items = append(items, item)
	}

	return items, nil
}

func (service *Service) SignRequest(ctx context.Context, organizationID, actorUserID, userEmail, requestID, signatureType, signatureData, ipAddress, userAgent string) error {
	signatureData = strings.TrimSpace(signatureData)
	if err := validateSignature(signatureType, signatureData); err != nil {
		return err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var recipientID string
	err = tx.QueryRow(ctx, `UPDATE signature_recipients r SET status='completed', signed_at=now(), signature_type=$8, signature_data=$4, signer_ip=$5, signer_user_agent=$6, consent_at=now(), user_id=NULLIF($2,'')::uuid WHERE r.signature_request_id=$1 AND LOWER(r.recipient_email)=LOWER($3) AND r.status IN ('pending','sent') AND EXISTS (SELECT 1 FROM signature_requests sr WHERE sr.id=r.signature_request_id AND sr.organization_id=$7 AND sr.status IN ('pending','sent')) RETURNING r.id::text`, requestID, actorUserID, userEmail, signatureData, ipAddress, userAgent, organizationID, signatureType).Scan(&recipientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	}
	if err != nil {
		return err
	}
	var pendingCount int
	if err = tx.QueryRow(ctx, `SELECT COUNT(*) FROM signature_recipients WHERE signature_request_id = $1 AND status != 'completed'`, requestID).Scan(&pendingCount); err != nil {
		return err
	}
	if pendingCount == 0 {
		if _, err = tx.Exec(ctx, `UPDATE signature_requests SET status='completed', completed_at=now() WHERE id=$1 AND organization_id=$2`, requestID, organizationID); err != nil {
			return err
		}
	}
	signatureHash := sha256.Sum256([]byte(signatureData))
	if _, err = tx.Exec(ctx, `INSERT INTO signature_events (signature_request_id, recipient_id, actor_user_id, event_type, detail, ip_address) VALUES ($1,$2,NULLIF($3,'')::uuid,'recipient_signed',$4,$5)`, requestID, recipientID, actorUserID, "sha256:"+hex.EncodeToString(signatureHash[:]), ipAddress); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,ip_address,user_agent,metadata) VALUES ($1,NULLIF($2,'')::uuid,'signature.recipient_signed','signature_request',$3,'unknown',NULLIF($4,'')::inet,$5,jsonb_build_object('signatureHash',$6::text))`, organizationID, actorUserID, requestID, ipAddress, userAgent, hex.EncodeToString(signatureHash[:])); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validateSignature(signatureType, signatureData string) error {
	if signatureType == "typed" {
		if len(signatureData) < 2 || len(signatureData) > 200 {
			return fmt.Errorf("typed signature must contain between 2 and 200 characters")
		}
	} else if signatureType == "drawn" || signatureType == "uploaded" {
		if len(signatureData) > 700_000 {
			return fmt.Errorf("signature image is too large")
		}
		mediaType, encoded, ok := strings.Cut(signatureData, ",")
		if !ok || (mediaType != "data:image/png;base64" && mediaType != "data:image/jpeg;base64") {
			return fmt.Errorf("signature must be a PNG or JPEG image")
		}
		imageBytes, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(imageBytes) > 512_000 {
			return fmt.Errorf("signature image is invalid or too large")
		}
		config, _, err := image.DecodeConfig(bytes.NewReader(imageBytes))
		if err != nil || config.Width < 2 || config.Height < 2 || config.Width > 2400 || config.Height > 1200 {
			return fmt.Errorf("signature image must be a valid PNG or JPEG up to 2400 by 1200 pixels")
		}
	} else {
		return fmt.Errorf("signature type must be typed, drawn, or uploaded")
	}
	return nil
}

func (service *Service) DeclineRequest(ctx context.Context, organizationID, actorUserID, userEmail, requestID, reason, ipAddress string) error {
	if strings.TrimSpace(reason) == "" || len(reason) > 2000 {
		return fmt.Errorf("decline reason must contain between 1 and 2000 characters")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var recipientID string
	err = tx.QueryRow(ctx, `UPDATE signature_recipients r SET status='declined', decline_reason=$4, declined_at=now(), user_id=$2 WHERE r.signature_request_id=$1 AND LOWER(r.recipient_email)=LOWER($3) AND r.status IN ('pending','sent') AND EXISTS (SELECT 1 FROM signature_requests sr WHERE sr.id=r.signature_request_id AND sr.organization_id=$5 AND sr.status IN ('pending','sent')) RETURNING r.id::text`, requestID, actorUserID, userEmail, reason, organizationID).Scan(&recipientID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRecipientNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE signature_requests SET status='declined' WHERE id=$1 AND organization_id=$2`, requestID, organizationID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO signature_events (signature_request_id,recipient_id,actor_user_id,event_type,detail,ip_address) VALUES ($1,$2,$3,'recipient_declined',$4,$5)`, requestID, recipientID, actorUserID, reason, ipAddress); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id,actor_user_id,action,resource_type,resource_id,request_id,ip_address,metadata) VALUES ($1,$2,'signature.recipient_declined','signature_request',$3,'unknown',NULLIF($4,'')::inet,'{}'::jsonb)`, organizationID, actorUserID, requestID, ipAddress); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (service *Service) CreateRequestFromTemplate(ctx context.Context, organizationID, actorUserID string, docTitle string, message string, recipients []CreateRecipient) (SignatureRequest, error) {
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return SignatureRequest{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := validateRecipients(recipients); err != nil {
		return SignatureRequest{}, err
	}
	// A signature request must reference an uploaded, available document.
	var docID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM documents
		WHERE organization_id = $1 AND filename = $2 AND status = 'available'
		LIMIT 1
	`, organizationID, docTitle).Scan(&docID)

	if err != nil {
		return SignatureRequest{}, ErrNotFound
	}

	// 2. Insert signature request
	var reqID string
	err = tx.QueryRow(ctx, `
		INSERT INTO signature_requests (organization_id, document_id, provider, status, custom_message, created_by_user_id)
		VALUES ($1, $2, 'native', 'pending', $3, $4)
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
			VALUES ($1, $2, $3, 'pending', $4)
			RETURNING id::text
		`, reqID, r.Email, r.Name, i+1).Scan(&recID)
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
		DocumentID: docID,
		Status:     "pending",
		Recipients: responseRecipients,
	}, nil
}
