package operations

import (
	"context"
)

type AuditEvent struct {
	ID             string  `json:"id"`
	OrganizationID *string `json:"organizationId"`
	AggregateID    string  `json:"aggregateId"`
	AggregateType  string  `json:"aggregateType"`
	EventType      string  `json:"eventType"`
	UserID         *string `json:"userId"`
	Timestamp      string  `json:"timestamp"`
}

func (s *Service) ListPlatformAuditLogs(ctx context.Context) ([]AuditEvent, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, aggregate_id::text, aggregate_type, event_type, user_id::text, created_at::text FROM audit_events ORDER BY created_at DESC LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []AuditEvent
	for rows.Next() {
		var a AuditEvent
		if err := rows.Scan(&a.ID, &a.OrganizationID, &a.AggregateID, &a.AggregateType, &a.EventType, &a.UserID, &a.Timestamp); err != nil {
			return nil, err
		}
		items = append(items, a)
	}
	return items, nil
}
