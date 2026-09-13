package support

import (
	"context"
)

type TenantTicket struct {
	ID                   string  `json:"id"`
	OrganizationID       string  `json:"organizationId"`
	EmployeeID           string  `json:"employeeId"`
	Subject              string  `json:"subject"`
	Priority             string  `json:"priority"`
	Status               string  `json:"status"`
	AssignedToEmployeeID *string `json:"assignedToEmployeeId"`
}

type CreateTenantTicketInput struct {
	Subject  string `json:"subject"`
	Message  string `json:"message"`
	Priority string `json:"priority"`
}

type CreateTicketMessageInput struct {
	Message string `json:"message"`
}

func (s *Service) ListTenantTickets(ctx context.Context, orgID string) ([]TenantTicket, error) {
	rows, err := s.pool.Query(ctx, "SELECT id::text, organization_id::text, employee_id::text, subject, priority, status, assigned_to_employee_id::text FROM tenant_support_tickets WHERE organization_id = $1 ORDER BY created_at DESC", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []TenantTicket
	for rows.Next() {
		var t TenantTicket
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.EmployeeID, &t.Subject, &t.Priority, &t.Status, &t.AssignedToEmployeeID); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, nil
}

func (s *Service) CreateTenantTicket(ctx context.Context, orgID, employeeID string, input CreateTenantTicketInput) (TenantTicket, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var t TenantTicket
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_support_tickets (organization_id, employee_id, subject, priority)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, organization_id::text, employee_id::text, subject, priority, status
	`, orgID, employeeID, input.Subject, input.Priority).Scan(
		&t.ID, &t.OrganizationID, &t.EmployeeID, &t.Subject, &t.Priority, &t.Status,
	)
	if err != nil {
		return TenantTicket{}, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO tenant_support_messages (ticket_id, sender_employee_id, message)
		VALUES ($1, $2, $3)
	`, t.ID, employeeID, input.Message)
	if err != nil {
		return TenantTicket{}, err
	}

	return t, tx.Commit(ctx)
}

func (s *Service) AddTenantMessage(ctx context.Context, orgID, employeeID, ticketID string, input CreateTicketMessageInput) error {
	// First ensure the ticket belongs to the org
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tenant_support_tickets WHERE id = $1 AND organization_id = $2)", ticketID, orgID).Scan(&exists)
	if err != nil || !exists {
		return err // Could return NotFound
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO tenant_support_messages (ticket_id, sender_employee_id, message)
		VALUES ($1, $2, $3)
	`, ticketID, employeeID, input.Message)
	return err
}
