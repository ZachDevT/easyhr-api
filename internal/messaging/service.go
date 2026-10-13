package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// ─── Domain types ─────────────────────────────────────────────────────────────

type EmailMacro struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organizationId"`
	Title          string     `json:"title"`
	Shortcut       string     `json:"shortcut"`
	Category       string     `json:"category"`
	Subject        string     `json:"subject"`
	BodyHTML       string     `json:"bodyHtml"`
	CreatedBy      *string    `json:"createdBy,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type EmailThread struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId"`
	ApplicantID    string    `json:"applicantId"`
	Subject        string    `json:"subject"`
	CandidateEmail string    `json:"candidateEmail"`
	CandidateName  string    `json:"candidateName"`
	Status         string    `json:"status"`
	LastMessageAt  time.Time `json:"lastMessageAt"`
	CreatedAt      time.Time `json:"createdAt"`
	MessageCount   int       `json:"messageCount"`
}

type EmailMessage struct {
	ID             string    `json:"id"`
	ThreadID       string    `json:"threadId"`
	OrganizationID string    `json:"organizationId"`
	SenderType     string    `json:"senderType"` // "recruiter" | "candidate"
	SenderName     string    `json:"senderName"`
	SenderEmail    string    `json:"senderEmail"`
	RecipientEmail string    `json:"recipientEmail"`
	Subject        string    `json:"subject"`
	BodyHTML       string    `json:"bodyHtml"`
	ResendID       *string   `json:"resendId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

// ─── Input types ──────────────────────────────────────────────────────────────

type SendEmailInput struct {
	Subject        string
	BodyHTML       string
	CandidateEmail string
	CandidateName  string
	SenderName     string
	SenderEmail    string
}

type CreateMacroInput struct {
	Title    string `json:"title"`
	Shortcut string `json:"shortcut"`
	Category string `json:"category"`
	Subject  string `json:"subject"`
	BodyHTML string `json:"bodyHtml"`
}

type UpdateMacroInput struct {
	Title    string `json:"title"`
	Shortcut string `json:"shortcut"`
	Category string `json:"category"`
	Subject  string `json:"subject"`
	BodyHTML string `json:"bodyHtml"`
}

// ─── EmailSender interface ────────────────────────────────────────────────────

type EmailSender interface {
	SendRaw(ctx context.Context, toEmail, subject, htmlBody string) error
}

// ─── Service ──────────────────────────────────────────────────────────────────

type Service struct {
	pool   *pgxpool.Pool
	mailer EmailSender
}

func NewService(pool *pgxpool.Pool, mailer EmailSender) *Service {
	return &Service{pool: pool, mailer: mailer}
}

// ─── Threads ──────────────────────────────────────────────────────────────────

// CreateThread creates a new email thread and sends the first message.
func (s *Service) CreateThread(ctx context.Context, orgID, applicantID, senderUserID string, input SendEmailInput) (EmailThread, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailThread{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var thread EmailThread
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_threads
			(organization_id, applicant_id, subject, candidate_email, candidate_name, status, last_message_at)
		VALUES ($1, $2, $3, $4, $5, 'open', NOW())
		RETURNING id::text, organization_id::text, applicant_id::text, subject, candidate_email, candidate_name, status, last_message_at, created_at
	`, orgID, applicantID, input.Subject, input.CandidateEmail, input.CandidateName).
		Scan(&thread.ID, &thread.OrganizationID, &thread.ApplicantID, &thread.Subject,
			&thread.CandidateEmail, &thread.CandidateName, &thread.Status, &thread.LastMessageAt, &thread.CreatedAt)
	if err != nil {
		return EmailThread{}, fmt.Errorf("insert thread: %w", err)
	}
	thread.MessageCount = 1

	_, err = tx.Exec(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html)
		VALUES ($1, $2, 'recruiter', $3, $4, $5, $6, $7)
	`, thread.ID, orgID, input.SenderName, input.SenderEmail, input.CandidateEmail, input.Subject, input.BodyHTML)
	if err != nil {
		return EmailThread{}, fmt.Errorf("insert message: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return EmailThread{}, fmt.Errorf("commit tx: %w", err)
	}

	// Fire email asynchronously
	if s.mailer != nil {
		go func() {
			_ = s.mailer.SendRaw(context.Background(), input.CandidateEmail, input.Subject, input.BodyHTML)
		}()
	}

	return thread, nil
}

// ReplyToThread appends a recruiter reply to an existing thread and re-sends to candidate.
func (s *Service) ReplyToThread(ctx context.Context, orgID, threadID, senderUserID, senderName, senderEmail, bodyHTML string) (EmailMessage, error) {
	var thread EmailThread
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, subject, candidate_email, candidate_name, status
		FROM candidate_email_threads
		WHERE id = $1 AND organization_id = $2
	`, threadID, orgID).Scan(&thread.ID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailMessage{}, ErrNotFound
	}
	if err != nil {
		return EmailMessage{}, fmt.Errorf("load thread: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var msg EmailMessage
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html)
		VALUES ($1, $2, 'recruiter', $3, $4, $5, $6, $7)
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, created_at
	`, threadID, orgID, senderName, senderEmail, thread.CandidateEmail, thread.Subject, bodyHTML).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.CreatedAt)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("insert message: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE candidate_email_threads SET last_message_at = NOW(), status = 'open' WHERE id = $1
	`, threadID)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("update thread: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return EmailMessage{}, fmt.Errorf("commit tx: %w", err)
	}

	if s.mailer != nil {
		go func() {
			_ = s.mailer.SendRaw(context.Background(), thread.CandidateEmail, thread.Subject, bodyHTML)
		}()
	}

	return msg, nil
}

// RecordCandidateReply records an incoming message from the candidate in the thread.
func (s *Service) RecordCandidateReply(ctx context.Context, orgID, threadID, senderName, bodyHTML string) (EmailMessage, error) {
	var thread EmailThread
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, organization_id::text, subject, candidate_email, candidate_name, status
		FROM candidate_email_threads
		WHERE id = $1 AND organization_id = $2
	`, threadID, orgID).Scan(&thread.ID, &thread.OrganizationID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailMessage{}, ErrNotFound
	}
	if err != nil {
		return EmailMessage{}, fmt.Errorf("load thread: %w", err)
	}

	if senderName == "" {
		senderName = thread.CandidateName
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var msg EmailMessage
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html)
		VALUES ($1, $2, 'candidate', $3, $4, $5, $6, $7)
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, created_at
	`, threadID, orgID, senderName, thread.CandidateEmail, "notifications@recruter.easyhr.pro", thread.Subject, bodyHTML).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.CreatedAt)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("insert candidate message: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE candidate_email_threads SET last_message_at = NOW(), status = 'open' WHERE id = $1
	`, threadID)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("update thread: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return EmailMessage{}, fmt.Errorf("commit tx: %w", err)
	}

	return msg, nil
}

// InboundWebhookMessage finds a thread (by ID or candidate email) and appends the incoming email.
func (s *Service) InboundWebhookMessage(ctx context.Context, threadID, senderEmail, senderName, subject, bodyHTML string) (EmailMessage, error) {
	var thread EmailThread
	var err error

	if threadID != "" {
		err = s.pool.QueryRow(ctx, `
			SELECT id::text, organization_id::text, subject, candidate_email, candidate_name, status
			FROM candidate_email_threads
			WHERE id = $1
		`, threadID).Scan(&thread.ID, &thread.OrganizationID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
	} else if senderEmail != "" {
		err = s.pool.QueryRow(ctx, `
			SELECT id::text, organization_id::text, subject, candidate_email, candidate_name, status
			FROM candidate_email_threads
			WHERE LOWER(candidate_email) = LOWER($1)
			ORDER BY last_message_at DESC
			LIMIT 1
		`, senderEmail).Scan(&thread.ID, &thread.OrganizationID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
	} else {
		return EmailMessage{}, errors.New("either threadId or senderEmail is required")
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return EmailMessage{}, ErrNotFound
	}
	if err != nil {
		return EmailMessage{}, fmt.Errorf("find thread for webhook: %w", err)
	}

	if senderName == "" {
		senderName = thread.CandidateName
	}
	if senderEmail == "" {
		senderEmail = thread.CandidateEmail
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var msg EmailMessage
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html)
		VALUES ($1, $2, 'candidate', $3, $4, $5, $6, $7)
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, created_at
	`, thread.ID, thread.OrganizationID, senderName, senderEmail, "recruiting@easyhr.app", subject, bodyHTML).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.CreatedAt)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("insert webhook message: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE candidate_email_threads SET last_message_at = NOW(), status = 'open' WHERE id = $1
	`, thread.ID)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("update thread: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return EmailMessage{}, fmt.Errorf("commit tx: %w", err)
	}

	return msg, nil
}

// ListThreads lists email threads for an org, optionally filtered by applicant.
func (s *Service) ListThreads(ctx context.Context, orgID, applicantID string) ([]EmailThread, error) {
	var query string
	var rows pgx.Rows
	var err error

	baseQuery := `
		SELECT t.id::text, t.organization_id::text, t.applicant_id::text, t.subject, t.candidate_email, t.candidate_name,
			t.status, t.last_message_at, t.created_at,
			COUNT(m.id) AS message_count
		FROM candidate_email_threads t
		LEFT JOIN candidate_email_messages m ON m.thread_id = t.id
		WHERE t.organization_id = $1
	`
	if applicantID != "" {
		query = baseQuery + ` AND t.applicant_id = $2 GROUP BY t.id ORDER BY t.last_message_at DESC`
		rows, err = s.pool.Query(ctx, query, orgID, applicantID)
	} else {
		query = baseQuery + ` GROUP BY t.id ORDER BY t.last_message_at DESC`
		rows, err = s.pool.Query(ctx, query, orgID)
	}
	if err != nil {
		return nil, fmt.Errorf("query threads: %w", err)
	}
	defer rows.Close()

	var threads []EmailThread
	for rows.Next() {
		var t EmailThread
		if err := rows.Scan(&t.ID, &t.OrganizationID, &t.ApplicantID, &t.Subject,
			&t.CandidateEmail, &t.CandidateName, &t.Status, &t.LastMessageAt, &t.CreatedAt, &t.MessageCount); err != nil {
			return nil, fmt.Errorf("scan thread: %w", err)
		}
		threads = append(threads, t)
	}
	if threads == nil {
		threads = []EmailThread{}
	}
	return threads, nil
}

// GetThreadMessages fetches all messages in a thread.
func (s *Service) GetThreadMessages(ctx context.Context, orgID, threadID string) ([]EmailMessage, error) {
	var count int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM candidate_email_threads WHERE id=$1 AND organization_id=$2`, threadID, orgID).Scan(&count)
	if err != nil || count == 0 {
		return nil, ErrNotFound
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, created_at
		FROM candidate_email_messages
		WHERE thread_id = $1
		ORDER BY created_at ASC
	`, threadID)
	if err != nil {
		return nil, fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var msgs []EmailMessage
	for rows.Next() {
		var m EmailMessage
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.OrganizationID, &m.SenderType, &m.SenderName,
			&m.SenderEmail, &m.RecipientEmail, &m.Subject, &m.BodyHTML, &m.ResendID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		msgs = append(msgs, m)
	}
	if msgs == nil {
		msgs = []EmailMessage{}
	}
	return msgs, nil
}

// ─── Macros ───────────────────────────────────────────────────────────────────

func (s *Service) ListMacros(ctx context.Context, orgID string) ([]EmailMacro, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, organization_id::text, title, shortcut, category, subject, body_html, created_by::text, created_at, updated_at
		FROM email_macros
		WHERE organization_id = $1
		ORDER BY category, title
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("query macros: %w", err)
	}
	defer rows.Close()

	var macros []EmailMacro
	for rows.Next() {
		var m EmailMacro
		if err := rows.Scan(&m.ID, &m.OrganizationID, &m.Title, &m.Shortcut, &m.Category,
			&m.Subject, &m.BodyHTML, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan macro: %w", err)
		}
		macros = append(macros, m)
	}
	if macros == nil {
		macros = []EmailMacro{}
	}
	return macros, nil
}

func (s *Service) CreateMacro(ctx context.Context, orgID, userID string, input CreateMacroInput) (EmailMacro, error) {
	var m EmailMacro
	err := s.pool.QueryRow(ctx, `
		INSERT INTO email_macros (organization_id, title, shortcut, category, subject, body_html, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id::text, organization_id::text, title, shortcut, category, subject, body_html, created_by::text, created_at, updated_at
	`, orgID, input.Title, input.Shortcut, input.Category, input.Subject, input.BodyHTML, userID).
		Scan(&m.ID, &m.OrganizationID, &m.Title, &m.Shortcut, &m.Category, &m.Subject, &m.BodyHTML, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if err != nil {
		return EmailMacro{}, fmt.Errorf("insert macro: %w", err)
	}
	return m, nil
}

func (s *Service) UpdateMacro(ctx context.Context, orgID, macroID string, input UpdateMacroInput) (EmailMacro, error) {
	var m EmailMacro
	err := s.pool.QueryRow(ctx, `
		UPDATE email_macros
		SET title=$3, shortcut=$4, category=$5, subject=$6, body_html=$7, updated_at=NOW()
		WHERE id=$1 AND organization_id=$2
		RETURNING id::text, organization_id::text, title, shortcut, category, subject, body_html, created_by::text, created_at, updated_at
	`, macroID, orgID, input.Title, input.Shortcut, input.Category, input.Subject, input.BodyHTML).
		Scan(&m.ID, &m.OrganizationID, &m.Title, &m.Shortcut, &m.Category, &m.Subject, &m.BodyHTML, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailMacro{}, ErrNotFound
	}
	if err != nil {
		return EmailMacro{}, fmt.Errorf("update macro: %w", err)
	}
	return m, nil
}

func (s *Service) DeleteMacro(ctx context.Context, orgID, macroID string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM email_macros WHERE id=$1 AND organization_id=$2`, macroID, orgID)
	if err != nil {
		return fmt.Errorf("delete macro: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
