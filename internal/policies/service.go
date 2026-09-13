package policies

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

type Policy struct {
	ID                string  `json:"id"`
	DocumentID        *string `json:"documentId"`
	Title             string  `json:"title"`
	Category          string  `json:"category"`
	RequiresSignature bool    `json:"requiresSignature"`
	Status            string  `json:"status"`
}

type CreatePolicyInput struct {
	DocumentID        *string `json:"documentId"`
	Title             string  `json:"title"`
	Category          string  `json:"category"`
	RequiresSignature bool    `json:"requiresSignature"`
}

func (s *Service) ListPolicies(ctx context.Context, orgID string) ([]Policy, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, document_id::text, title, category, requires_signature, status FROM company_policies WHERE organization_id = $1 ORDER BY created_at DESC", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []Policy
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.DocumentID, &p.Title, &p.Category, &p.RequiresSignature, &p.Status); err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, nil
}

func (s *Service) CreatePolicy(ctx context.Context, orgID, userID string, input CreatePolicyInput) (Policy, error) {
	var p Policy
	err := s.pool.QueryRow(ctx, `
		INSERT INTO company_policies (organization_id, document_id, title, category, requires_signature, created_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id::text, document_id::text, title, category, requires_signature, status
	`, orgID, input.DocumentID, input.Title, input.Category, input.RequiresSignature, userID).Scan(
		&p.ID, &p.DocumentID, &p.Title, &p.Category, &p.RequiresSignature, &p.Status,
	)
	return p, err
}
