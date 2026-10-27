package documents

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDocumentNotFound = errors.New("document not found")
var ErrDocumentNotCompletable = errors.New("document cannot be completed")

var sha256Pattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

type Service struct {
	pool    *pgxpool.Pool
	storage Storage
}

func NewService(pool *pgxpool.Pool, storage Storage) *Service {
	return &Service{pool: pool, storage: storage}
}

type CreateUploadIntentInput struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	EmployeeID  string `json:"employeeId"`
	Category    string `json:"category"`
	ExpiresOn   string `json:"expiresOn"`
}

type Document struct {
	ID           string  `json:"id"`
	EmployeeID   *string `json:"employeeId"`
	EmployeeName string  `json:"employeeName,omitempty"`
	Filename     string  `json:"filename"`
	Category     string  `json:"category"`
	Status       string  `json:"status"`
	ExpiresOn    *string `json:"expiresOn"`
	CreatedAt    string  `json:"createdAt"`
}

type UploadIntent struct {
	Document  Document `json:"document"`
	UploadURL string   `json:"uploadUrl"`
	ExpiresAt string   `json:"expiresAt"`
}

func (service *Service) List(ctx context.Context, organizationID, employeeID string, limit int) ([]Document, error) {
	if limit < 1 || limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}

	var rows pgx.Rows
	var err error

	if employeeID != "" {
		rows, err = service.pool.Query(ctx, `
			SELECT d.id::text, d.employee_id::text, COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
				d.filename, d.category, d.status, d.expires_on::text, d.created_at::text
			FROM documents d
			LEFT JOIN employees e ON e.id = d.employee_id
			WHERE d.organization_id = $1 AND d.employee_id = $2 AND d.status <> 'deleted'
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT $3`, organizationID, employeeID, limit)
	} else {
		rows, err = service.pool.Query(ctx, `
			SELECT d.id::text, d.employee_id::text, COALESCE(e.first_name || ' ' || e.last_name, 'Team Member') as employee_name,
				d.filename, d.category, d.status, d.expires_on::text, d.created_at::text
			FROM documents d
			LEFT JOIN employees e ON e.id = d.employee_id
			WHERE d.organization_id = $1 AND d.status <> 'deleted'
			ORDER BY d.created_at DESC, d.id DESC
			LIMIT $2`, organizationID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Document, 0, limit)
	for rows.Next() {
		var item Document
		if err = rows.Scan(&item.ID, &item.EmployeeID, &item.EmployeeName, &item.Filename, &item.Category, &item.Status, &item.ExpiresOn, &item.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (input CreateUploadIntentInput) Validate() error {
	if name := strings.TrimSpace(input.Filename); name == "" || len(name) > 255 {
		return fmt.Errorf("filename must contain between 1 and 255 characters")
	}
	if contentType := strings.TrimSpace(input.ContentType); contentType == "" || len(contentType) > 120 || !strings.Contains(contentType, "/") {
		return fmt.Errorf("contentType must be a valid MIME type")
	}
	if input.SizeBytes < 1 || input.SizeBytes > 52_428_800 {
		return fmt.Errorf("sizeBytes must be between 1 and 52428800")
	}
	if !oneOf(input.Category, "contract", "identity", "compliance", "certification", "other") {
		return fmt.Errorf("category is invalid")
	}
	if input.ExpiresOn != "" {
		if _, err := time.Parse(time.DateOnly, input.ExpiresOn); err != nil {
			return fmt.Errorf("expiresOn must be an ISO date")
		}
	}
	return nil
}

func (service *Service) CreateUploadIntent(ctx context.Context, organizationID, actorUserID string, input CreateUploadIntentInput) (UploadIntent, error) {
	if service.storage == nil {
		return UploadIntent{}, fmt.Errorf("document storage is not configured")
	}
	if err := input.Validate(); err != nil {
		return UploadIntent{}, err
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return UploadIntent{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if input.EmployeeID != "" {
		var employeeExists bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM employees WHERE id = $1 AND organization_id = $2)`, input.EmployeeID, organizationID).Scan(&employeeExists); err != nil {
			return UploadIntent{}, err
		}
		if !employeeExists {
			return UploadIntent{}, ErrDocumentNotFound
		}
	}
	keySuffix, err := randomObjectKeySuffix()
	if err != nil {
		return UploadIntent{}, err
	}
	expiresAt := time.Now().UTC().Add(15 * time.Minute)
	objectKey := "organizations/" + organizationID + "/documents/" + keySuffix
	result := UploadIntent{ExpiresAt: expiresAt.Format(time.RFC3339)}
	err = tx.QueryRow(ctx, `
		INSERT INTO documents (organization_id, employee_id, filename, category, content_type, size_bytes, object_key, upload_expires_at, expires_on, created_by_user_id)
		VALUES ($1, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7, $8, NULLIF($9, '')::date, $10)
		RETURNING id::text, employee_id::text, filename, category, status, expires_on::text, created_at::text`,
		organizationID, input.EmployeeID, strings.TrimSpace(input.Filename), input.Category, strings.TrimSpace(input.ContentType), input.SizeBytes, objectKey, expiresAt, input.ExpiresOn, actorUserID).
		Scan(&result.Document.ID, &result.Document.EmployeeID, &result.Document.Filename, &result.Document.Category, &result.Document.Status, &result.Document.ExpiresOn, &result.Document.CreatedAt)
	if err != nil {
		return UploadIntent{}, err
	}
	result.UploadURL, err = service.storage.UploadURL(ctx, objectKey, strings.TrimSpace(input.ContentType), input.SizeBytes, 15*time.Minute)
	if err != nil {
		return UploadIntent{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id, metadata) VALUES ($1, $2, 'document.upload_intent_created', 'document', $3, 'unknown', jsonb_build_object('category', $4::text, 'sizeBytes', $5::bigint))`, organizationID, actorUserID, result.Document.ID, input.Category, input.SizeBytes); err != nil {
		return UploadIntent{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return UploadIntent{}, err
	}
	return result, nil
}

func (service *Service) CompleteUpload(ctx context.Context, organizationID, actorUserID, documentID, checksum string) (Document, error) {
	if service.storage == nil {
		return Document{}, fmt.Errorf("document storage is not configured")
	}
	if !sha256Pattern.MatchString(checksum) {
		return Document{}, fmt.Errorf("checksumSha256 must be a SHA-256 hex digest")
	}
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var objectKey string
	var sizeBytes int64
	var expiresAt time.Time
	result := Document{ID: documentID}
	err = tx.QueryRow(ctx, `SELECT employee_id::text, filename, category, status, expires_on::text, created_at::text, object_key, size_bytes, upload_expires_at FROM documents WHERE id = $1 AND organization_id = $2 FOR UPDATE`, documentID, organizationID).Scan(&result.EmployeeID, &result.Filename, &result.Category, &result.Status, &result.ExpiresOn, &result.CreatedAt, &objectKey, &sizeBytes, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Document{}, err
	}
	if result.Status == "available" || result.Status == "ready" {
		return result, nil
	}
	if result.Status != "pending_upload" && result.Status != "pending_scan" {
		return Document{}, ErrDocumentNotCompletable
	}
	if time.Now().UTC().After(expiresAt) {
		return Document{}, ErrDocumentNotCompletable
	}
	body, _, actualSize, err := service.storage.GetObject(ctx, objectKey)
	if err != nil {
		return Document{}, fmt.Errorf("uploaded document is not available in storage: %w", err)
	}
	defer body.Close()
	if actualSize != sizeBytes {
		return Document{}, fmt.Errorf("uploaded document size does not match upload intent")
	}
	digest := sha256.New()
	if _, err = io.Copy(digest, body); err != nil {
		return Document{}, fmt.Errorf("read uploaded document for verification: %w", err)
	}
	actualChecksum := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(actualChecksum, checksum) {
		return Document{}, fmt.Errorf("uploaded document checksum does not match")
	}
	if _, err = tx.Exec(ctx, `UPDATE documents SET status = 'available', sha256 = lower($2) WHERE id = $1`, documentID, checksum); err != nil {
		return Document{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO document_versions (document_id, version_number, object_key, sha256, size_bytes, created_by_user_id) VALUES ($1, 1, $2, lower($3), $4, $5) ON CONFLICT (document_id, version_number) DO NOTHING`, documentID, objectKey, checksum, sizeBytes, actorUserID); err != nil {
		return Document{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events (organization_id, event_type, aggregate_type, aggregate_id, payload) VALUES ($1, 'document.scan_requested', 'document', $2, jsonb_build_object('documentId', $2::text, 'objectKey', $3::text))`, organizationID, documentID, objectKey); err != nil {
		return Document{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_events (organization_id, actor_user_id, action, resource_type, resource_id, request_id) VALUES ($1, $2, 'document.upload_completed', 'document', $3, 'unknown')`, organizationID, actorUserID, documentID); err != nil {
		return Document{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Document{}, err
	}
	result.Status = "available"
	return result, nil
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func randomObjectKeySuffix() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate object key: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}
