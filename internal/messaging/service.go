package messaging

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/easyhr/easyhr-be/internal/email"
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
	MessageID      *string   `json:"messageId,omitempty"`
	InReplyTo      *string   `json:"inReplyTo,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
}

type InboundEmailRecord struct {
	ID              string     `json:"id"`
	ResendEmailID   string     `json:"resendEmailId"`
	SenderEmail     string     `json:"senderEmail"`
	SenderName      *string    `json:"senderName,omitempty"`
	RecipientEmail  string     `json:"recipientEmail"`
	Subject         string     `json:"subject"`
	MessageID       *string    `json:"messageId,omitempty"`
	InReplyTo       *string    `json:"inReplyTo,omitempty"`
	BodyHTML        string     `json:"bodyHtml"`
	BodyText        string     `json:"bodyText"`
	Status          string     `json:"status"`
	MatchedThreadID *string    `json:"matchedThreadId,omitempty"`
	OrganizationID  *string    `json:"organizationId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type ProcessInboundInput struct {
	ResendEmailID  string
	SenderEmail    string
	SenderName     string
	RecipientEmail string
	Subject        string
	BodyHTML       string
	BodyText       string
	MessageID      string
	InReplyTo      string
	RawJSON        []byte
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
	SendRawWithHeaders(ctx context.Context, toEmail, subject, htmlBody string, headers map[string]string) (string, error)
	RetrieveReceivedEmail(ctx context.Context, emailID string) (*email.ReceivedEmailDetails, error)
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
			resendID, err := s.mailer.SendRawWithHeaders(context.Background(), input.CandidateEmail, input.Subject, input.BodyHTML, nil)
			if err == nil && resendID != "" {
				_, _ = s.pool.Exec(context.Background(), `
					UPDATE candidate_email_messages SET resend_id = $1 WHERE thread_id = $2
				`, resendID, thread.ID)
			}
		}()
	}

	return thread, nil
}

// ReplyToThread appends a recruiter reply to an existing thread and re-sends to candidate with In-Reply-To headers.
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

	// Look up the last candidate message or last message in this thread for threading headers
	var inReplyToHeader string
	var lastMsgID, lastResendID *string
	_ = s.pool.QueryRow(ctx, `
		SELECT message_id, resend_id
		FROM candidate_email_messages
		WHERE thread_id = $1 AND sender_type = 'candidate'
		ORDER BY created_at DESC
		LIMIT 1
	`, threadID).Scan(&lastMsgID, &lastResendID)

	if lastMsgID != nil && *lastMsgID != "" {
		inReplyToHeader = *lastMsgID
	} else if lastResendID != nil && *lastResendID != "" {
		inReplyToHeader = fmt.Sprintf("<%s@recruter.easyhr.pro>", *lastResendID)
	}

	replySubject := thread.Subject
	if !strings.HasPrefix(strings.ToLower(replySubject), "re:") {
		replySubject = "Re: " + replySubject
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var msg EmailMessage
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html, in_reply_to)
		VALUES ($1, $2, 'recruiter', $3, $4, $5, $6, $7, $8)
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, message_id, in_reply_to, created_at
	`, threadID, orgID, senderName, senderEmail, thread.CandidateEmail, replySubject, bodyHTML, inReplyToHeader).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.ResendID, &msg.MessageID, &msg.InReplyTo, &msg.CreatedAt)
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

	headers := make(map[string]string)
	if inReplyToHeader != "" {
		headers["In-Reply-To"] = inReplyToHeader
		headers["References"] = inReplyToHeader
	}

	if s.mailer != nil {
		go func() {
			resendID, err := s.mailer.SendRawWithHeaders(context.Background(), thread.CandidateEmail, replySubject, bodyHTML, headers)
			if err == nil && resendID != "" {
				_, _ = s.pool.Exec(context.Background(), `
					UPDATE candidate_email_messages SET resend_id = $1 WHERE id = $2
				`, resendID, msg.ID)
			}
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
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, message_id, in_reply_to, created_at
	`, threadID, orgID, senderName, thread.CandidateEmail, "notifications@recruter.easyhr.pro", thread.Subject, bodyHTML).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.ResendID, &msg.MessageID, &msg.InReplyTo, &msg.CreatedAt)
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

// ProcessInboundEmail processes an inbound email event from Resend, parses headers, matches thread or applicant, records to inbound_emails, and appends to candidate thread.
func (s *Service) ProcessInboundEmail(ctx context.Context, input ProcessInboundInput) (EmailMessage, error) {
	senderEmail := strings.TrimSpace(input.SenderEmail)
	senderName := strings.TrimSpace(input.SenderName)
	if senderName == "" {
		senderName = senderEmail
	}
	bodyHTML := input.BodyHTML
	if strings.TrimSpace(bodyHTML) == "" {
		if strings.TrimSpace(input.BodyText) != "" {
			bodyHTML = fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(input.BodyText, "\n", "<br/>"))
		} else {
			bodyHTML = "<p>No content</p>"
		}
	}

	// Deduplication: If this exact Resend email event was already processed, return existing message
	if input.ResendEmailID != "" {
		var existingMsgID, existingThreadID, existingOrgID, existingType, existingName, existingSender, existingRecipient, existingSub, existingBody string
		var existingCreated time.Time
		errExists := s.pool.QueryRow(ctx, `
			SELECT id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, created_at
			FROM candidate_email_messages
			WHERE resend_id = $1
			LIMIT 1
		`, input.ResendEmailID).Scan(&existingMsgID, &existingThreadID, &existingOrgID, &existingType, &existingName, &existingSender, &existingRecipient, &existingSub, &existingBody, &existingCreated)
		if errExists == nil && existingMsgID != "" {
			return EmailMessage{
				ID:             existingMsgID,
				ThreadID:       existingThreadID,
				OrganizationID: existingOrgID,
				SenderType:     existingType,
				SenderName:     existingName,
				SenderEmail:    existingSender,
				RecipientEmail: existingRecipient,
				Subject:        existingSub,
				BodyHTML:       existingBody,
				CreatedAt:      existingCreated,
			}, nil
		}
	}

	var thread EmailThread
	var matchedOrgID string
	matched := false

	// Strategy 1: Match by In-Reply-To header
	if input.InReplyTo != "" {
		cleanInReplyTo := strings.Trim(input.InReplyTo, "<>")
		err := s.pool.QueryRow(ctx, `
			SELECT t.id::text, t.organization_id::text, t.applicant_id::text, t.subject, t.candidate_email, t.candidate_name, t.status
			FROM candidate_email_messages m
			JOIN candidate_email_threads t ON t.id = m.thread_id
			WHERE m.message_id = $1 OR m.resend_id = $1 OR m.message_id = $2 OR m.resend_id = $2
			LIMIT 1
		`, input.InReplyTo, cleanInReplyTo).Scan(&thread.ID, &thread.OrganizationID, &thread.ApplicantID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
		if err == nil && thread.ID != "" {
			matched = true
			matchedOrgID = thread.OrganizationID
		}
	}

	// Strategy 2: Match by existing candidate email in threads
	if !matched && senderEmail != "" {
		err := s.pool.QueryRow(ctx, `
			SELECT id::text, organization_id::text, applicant_id::text, subject, candidate_email, candidate_name, status
			FROM candidate_email_threads
			WHERE LOWER(candidate_email) = LOWER($1)
			ORDER BY last_message_at DESC
			LIMIT 1
		`, senderEmail).Scan(&thread.ID, &thread.OrganizationID, &thread.ApplicantID, &thread.Subject, &thread.CandidateEmail, &thread.CandidateName, &thread.Status)
		if err == nil && thread.ID != "" {
			matched = true
			matchedOrgID = thread.OrganizationID
		}
	}

	// Strategy 3: Match against job_applicants table joined with job_postings for organization_id
	if !matched && senderEmail != "" {
		var appID, orgID, appName, appEmail string
		err := s.pool.QueryRow(ctx, `
			SELECT a.id::text, p.organization_id::text, TRIM(a.first_name || ' ' || a.last_name), a.email
			FROM job_applicants a
			JOIN job_postings p ON p.id = a.job_posting_id
			WHERE LOWER(a.email) = LOWER($1)
			ORDER BY a.created_at DESC
			LIMIT 1
		`, senderEmail).Scan(&appID, &orgID, &appName, &appEmail)
		if err == nil && appID != "" {
			sub := input.Subject
			if sub == "" {
				sub = "Conversation with " + appName
			}
			err = s.pool.QueryRow(ctx, `
				INSERT INTO candidate_email_threads
					(organization_id, applicant_id, subject, candidate_email, candidate_name, status, last_message_at)
				VALUES ($1, $2, $3, $4, $5, 'open', NOW())
				RETURNING id::text, organization_id::text, applicant_id::text, subject, candidate_email, candidate_name, status, last_message_at, created_at
			`, orgID, appID, sub, appEmail, appName).
				Scan(&thread.ID, &thread.OrganizationID, &thread.ApplicantID, &thread.Subject,
					&thread.CandidateEmail, &thread.CandidateName, &thread.Status, &thread.LastMessageAt, &thread.CreatedAt)
			if err == nil {
				matched = true
				matchedOrgID = orgID
			}
		}
	}

	// Record in inbound_emails table
	status := "unmatched"
	var threadIDPtr *string
	var orgIDPtr *string
	if matched {
		status = "matched_thread"
		threadIDPtr = &thread.ID
		orgIDPtr = &matchedOrgID
	}

	rawJSON := input.RawJSON
	if len(rawJSON) == 0 {
		rawJSON = []byte("{}")
	}

	_, _ = s.pool.Exec(ctx, `
		INSERT INTO inbound_emails
			(resend_email_id, sender_email, sender_name, recipient_email, subject, message_id, in_reply_to, body_html, body_text, raw_payload, status, matched_thread_id, organization_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`, input.ResendEmailID, senderEmail, senderName, input.RecipientEmail, input.Subject, input.MessageID, input.InReplyTo, bodyHTML, input.BodyText, rawJSON, status, threadIDPtr, orgIDPtr)

	if !matched {
		return EmailMessage{}, fmt.Errorf("%w: no matching thread or candidate for sender %s", ErrNotFound, senderEmail)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var msg EmailMessage
	err = tx.QueryRow(ctx, `
		INSERT INTO candidate_email_messages
			(thread_id, organization_id, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, message_id, in_reply_to, raw_json)
		VALUES ($1, $2, 'candidate', $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, message_id, in_reply_to, created_at
	`, thread.ID, matchedOrgID, senderName, senderEmail, input.RecipientEmail, input.Subject, bodyHTML, input.ResendEmailID, input.MessageID, input.InReplyTo, rawJSON).
		Scan(&msg.ID, &msg.ThreadID, &msg.OrganizationID, &msg.SenderType, &msg.SenderName,
			&msg.SenderEmail, &msg.RecipientEmail, &msg.Subject, &msg.BodyHTML, &msg.ResendID, &msg.MessageID, &msg.InReplyTo, &msg.CreatedAt)
	if err != nil {
		return EmailMessage{}, fmt.Errorf("insert candidate inbound message: %w", err)
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

// InboundWebhookMessage backwards compatibility wrapper for manual inbound requests.
func (s *Service) InboundWebhookMessage(ctx context.Context, threadID, senderEmail, senderName, subject, bodyHTML string) (EmailMessage, error) {
	if threadID != "" {
		var orgID string
		err := s.pool.QueryRow(ctx, `SELECT organization_id::text FROM candidate_email_threads WHERE id = $1`, threadID).Scan(&orgID)
		if errors.Is(err, pgx.ErrNoRows) {
			return EmailMessage{}, ErrNotFound
		}
		if err != nil {
			return EmailMessage{}, err
		}
		return s.RecordCandidateReply(ctx, orgID, threadID, senderName, bodyHTML)
	}
	return s.ProcessInboundEmail(ctx, ProcessInboundInput{
		ResendEmailID:  fmt.Sprintf("manual-%d", time.Now().UnixNano()),
		SenderEmail:    senderEmail,
		SenderName:     senderName,
		RecipientEmail: "notifications@recruter.easyhr.pro",
		Subject:        subject,
		BodyHTML:       bodyHTML,
	})
}

// ListInboundEmails returns raw received inbound emails for an organization.
func (s *Service) ListInboundEmails(ctx context.Context, orgID string) ([]InboundEmailRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, resend_email_id, sender_email, sender_name, recipient_email, subject,
		       message_id, in_reply_to, body_html, body_text, status, matched_thread_id::text, organization_id::text, created_at
		FROM inbound_emails
		WHERE organization_id = $1 OR organization_id IS NULL
		ORDER BY created_at DESC
		LIMIT 100
	`, orgID)
	if err != nil {
		return nil, fmt.Errorf("query inbound emails: %w", err)
	}
	defer rows.Close()

	var records []InboundEmailRecord
	for rows.Next() {
		var r InboundEmailRecord
		if err := rows.Scan(&r.ID, &r.ResendEmailID, &r.SenderEmail, &r.SenderName, &r.RecipientEmail,
			&r.Subject, &r.MessageID, &r.InReplyTo, &r.BodyHTML, &r.BodyText, &r.Status,
			&r.MatchedThreadID, &r.OrganizationID, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan inbound email: %w", err)
		}
		records = append(records, r)
	}
	if records == nil {
		records = []InboundEmailRecord{}
	}
	return records, nil
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
		SELECT id::text, thread_id::text, organization_id::text, sender_type, sender_name, sender_email, recipient_email, subject, body_html, resend_id, message_id, in_reply_to, created_at
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
			&m.SenderEmail, &m.RecipientEmail, &m.Subject, &m.BodyHTML, &m.ResendID, &m.MessageID, &m.InReplyTo, &m.CreatedAt); err != nil {
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
