package documents

import (
	"context"
	"fmt"
	"time"
)

type DownloadUrl struct {
	URL         string `json:"url"`
	DownloadURL string `json:"downloadUrl,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ExpiresAt   string `json:"expiresAt"`
}

func (service *Service) GetDownloadUrl(ctx context.Context, organizationID, actorUserID, documentID string) (DownloadUrl, error) {
	if service.storage == nil {
		return DownloadUrl{}, fmt.Errorf("document storage is not configured")
	}
	var objectKey string
	var status string
	var filename string
	err := service.pool.QueryRow(ctx, `SELECT object_key, status, COALESCE(filename, '') FROM documents WHERE id = $1 AND organization_id = $2`, documentID, organizationID).Scan(&objectKey, &status, &filename)
	if err != nil {
		return DownloadUrl{}, ErrDocumentNotFound
	}
	if status != "available" && status != "ready" && status != "archived" && status != "pending_scan" {
		return DownloadUrl{}, fmt.Errorf("document is not ready for download")
	}

	url, err := service.storage.DownloadURL(ctx, objectKey, 15*time.Minute)
	if err != nil {
		return DownloadUrl{}, fmt.Errorf("could not generate download url: %w", err)
	}

	attachment, err := service.storage.AttachmentURL(ctx, objectKey, filename, 15*time.Minute)
	if err != nil {
		attachment = url
	}

	return DownloadUrl{
		URL:         url,
		DownloadURL: attachment,
		Filename:    filename,
		ExpiresAt:   time.Now().UTC().Add(15 * time.Minute).Format(time.RFC3339),
	}, nil
}
