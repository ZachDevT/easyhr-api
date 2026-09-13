package platform

import (
	"context"
	"time"
)

type SupportSession struct {
	ID             string `json:"id"`
	OperatorID     string `json:"operatorId"`
	OrganizationID string `json:"organizationId"`
	Reason         string `json:"reason"`
	Status         string `json:"status"`
	ExpiresAt      string `json:"expiresAt"`
}

type CreateSupportSessionInput struct {
	OrganizationID string `json:"organizationId"`
	Reason         string `json:"reason"`
}

func (s *Service) CreateSupportSession(ctx context.Context, operatorID string, input CreateSupportSessionInput) (SupportSession, error) {
	expiresAt := time.Now().Add(1 * time.Hour)

	var ss SupportSession
	err := s.pool.QueryRow(ctx, `
		INSERT INTO support_sessions (operator_id, organization_id, reason, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, operator_id::text, organization_id::text, reason, status, expires_at::text
	`, operatorID, input.OrganizationID, input.Reason, expiresAt).Scan(
		&ss.ID, &ss.OperatorID, &ss.OrganizationID, &ss.Reason, &ss.Status, &ss.ExpiresAt,
	)

	return ss, err
}

func (s *Service) HasPermission(ctx context.Context, operatorID, permissionKey string) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM platform_operator_roles por
		JOIN platform_role_permissions prp ON por.role_id = prp.role_id
		JOIN platform_permissions pp ON prp.permission_id = pp.id
		WHERE por.operator_id = $1 AND pp.key = $2
	`, operatorID, permissionKey).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
