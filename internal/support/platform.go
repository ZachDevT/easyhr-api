package support

import (
	"context"
)

type PublicContactInput struct {
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
}

type PlatformTicket struct {
	ID                   string  `json:"id"`
	OrganizationID       *string `json:"organizationId"`
	ContactEmail         *string `json:"contactEmail"`
	Subject              string  `json:"subject"`
	Priority             string  `json:"priority"`
	Status               string  `json:"status"`
	AssignedToOperatorID *string `json:"assignedToOperatorId"`
}

func (s *Service) SubmitPublicContact(ctx context.Context, input PublicContactInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ticketID string
	err = tx.QueryRow(ctx, `
		INSERT INTO platform_support_tickets (contact_email, subject, priority)
		VALUES ($1, $2, 'normal')
		RETURNING id::text
	`, input.Email, input.Subject).Scan(&ticketID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO platform_support_messages (ticket_id, sender_type, message)
		VALUES ($1, 'customer', $2)
	`, ticketID, input.Message)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Service) ListPlatformTickets(ctx context.Context) ([]PlatformTicket, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, contact_email, subject, priority, status, assigned_to_operator_id::text FROM platform_support_tickets ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []PlatformTicket
	for rows.Next() {
		var t PlatformTicket
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.ContactEmail, &t.Subject, &t.Priority, &t.Status, &t.AssignedToOperatorID); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, nil
}

func (s *Service) AddPlatformMessage(ctx context.Context, operatorID, ticketID string, input CreateTicketMessageInput) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO platform_support_messages (ticket_id, sender_type, sender_operator_id, message)
		VALUES ($1, 'operator', $2, $3)
	`, ticketID, operatorID, input.Message)
	return err
}
