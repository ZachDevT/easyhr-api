package support

import (
	"context"
)

type TenantTicket struct {
	ID                   string  `json:"id"`
	OrganizationID       string  `json:"organizationId"`
	EmployeeID           string  `json:"employeeId"`
	EmployeeName         string  `json:"employeeName"`
	EmployeeEmail        string  `json:"employeeEmail"`
	EmployeeAvatar       string  `json:"employeeAvatar"`
	Subject              string  `json:"subject"`
	Priority             string  `json:"priority"`
	Status               string  `json:"status"`
	AssignedToEmployeeID *string `json:"assignedToEmployeeId"`
	CreatedAt            string  `json:"createdAt"`
	LatestMessage        *string `json:"latestMessage,omitempty"`
}

type TenantTicketMessage struct {
	ID               string `json:"id"`
	TicketID         string `json:"ticketId"`
	SenderEmployeeID string `json:"senderEmployeeId"`
	SenderName       string `json:"senderName"`
	SenderAvatar     string `json:"senderAvatar"`
	Message          string `json:"message"`
	CreatedAt        string `json:"createdAt"`
}

type CreateTenantTicketInput struct {
	Subject  string `json:"subject"`
	Message  string `json:"message"`
	Priority string `json:"priority"`
}

type CreateTicketMessageInput struct {
	Message string `json:"message"`
}

type UpdateTicketStatusInput struct {
	Status string `json:"status"`
}

func (s *Service) ListTenantTickets(ctx context.Context, orgID string) ([]TenantTicket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 
			t.id::text, 
			t.organization_id::text, 
			t.employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Unknown Employee') AS employee_name,
			COALESCE(e.work_email, '') AS employee_email,
			COALESCE(p.avatar_url, '') AS employee_avatar,
			t.subject, 
			t.priority, 
			t.status, 
			t.assigned_to_employee_id::text,
			to_char(t.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at,
			(
				SELECT m.message 
				FROM tenant_support_messages m 
				WHERE m.ticket_id = t.id 
				ORDER BY m.created_at DESC 
				LIMIT 1
			) AS latest_message
		FROM tenant_support_tickets t
		LEFT JOIN employees e ON e.id = t.employee_id
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		WHERE t.organization_id = $1
		ORDER BY t.created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]TenantTicket, 0)
	for rows.Next() {
		var t TenantTicket
		if err := rows.Scan(
			&t.ID, &t.OrganizationID, &t.EmployeeID,
			&t.EmployeeName, &t.EmployeeEmail, &t.EmployeeAvatar,
			&t.Subject, &t.Priority, &t.Status, &t.AssignedToEmployeeID,
			&t.CreatedAt, &t.LatestMessage,
		); err != nil {
			return nil, err
		}
		items = append(items, t)
	}
	return items, nil
}

func (s *Service) ListTenantTicketMessages(ctx context.Context, orgID, ticketID string) ([]TenantTicketMessage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 
			m.id::text, 
			m.ticket_id::text, 
			m.sender_employee_id::text, 
			COALESCE(e.first_name || ' ' || e.last_name, 'Employee') AS sender_name,
			COALESCE(p.avatar_url, '') AS sender_avatar,
			m.message, 
			to_char(m.created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"') AS created_at
		FROM tenant_support_messages m
		JOIN tenant_support_tickets t ON t.id = m.ticket_id
		LEFT JOIN employees e ON e.id = m.sender_employee_id
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		WHERE m.ticket_id = $1 AND t.organization_id = $2
		ORDER BY m.created_at ASC
	`, ticketID, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]TenantTicketMessage, 0)
	for rows.Next() {
		var msg TenantTicketMessage
		if err := rows.Scan(
			&msg.ID, &msg.TicketID, &msg.SenderEmployeeID,
			&msg.SenderName, &msg.SenderAvatar, &msg.Message,
			&msg.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, msg)
	}
	return items, nil
}

func (s *Service) CreateTenantTicket(ctx context.Context, orgID, employeeID string, input CreateTenantTicketInput) (TenantTicket, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return TenantTicket{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	priority := input.Priority
	if priority == "" {
		priority = "normal"
	}

	var t TenantTicket
	err = tx.QueryRow(ctx, `
		INSERT INTO tenant_support_tickets (organization_id, employee_id, subject, priority)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, organization_id::text, employee_id::text, subject, priority, status, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	`, orgID, employeeID, input.Subject, priority).Scan(
		&t.ID, &t.OrganizationID, &t.EmployeeID, &t.Subject, &t.Priority, &t.Status, &t.CreatedAt,
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

	_ = tx.QueryRow(ctx, `
		SELECT COALESCE(e.first_name || ' ' || e.last_name, 'Employee'), COALESCE(e.work_email, ''), COALESCE(p.avatar_url, '')
		FROM employees e
		LEFT JOIN employee_personal_profiles p ON p.employee_id = e.id
		WHERE e.id = $1
	`, employeeID).Scan(&t.EmployeeName, &t.EmployeeEmail, &t.EmployeeAvatar)
	t.LatestMessage = &input.Message

	return t, tx.Commit(ctx)
}

func (s *Service) AddTenantMessage(ctx context.Context, orgID, employeeID, ticketID string, input CreateTicketMessageInput) error {
	var exists bool
	err := s.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM tenant_support_tickets WHERE id = $1 AND organization_id = $2)", ticketID, orgID).Scan(&exists)
	if err != nil || !exists {
		return err
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO tenant_support_messages (ticket_id, sender_employee_id, message)
		VALUES ($1, $2, $3)
	`, ticketID, employeeID, input.Message)
	return err
}

func (s *Service) UpdateTenantTicketStatus(ctx context.Context, orgID, ticketID, newStatus string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE tenant_support_tickets 
		SET status = $1, updated_at = now() 
		WHERE id = $2 AND organization_id = $3
	`, newStatus, ticketID, orgID)
	return err
}
