package chat

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/easyhr/easyhr-be/internal/documents"
	"github.com/easyhr/easyhr-be/internal/identity"
)

const (
	MaxAttachmentSize = 50 * 1024 * 1024 // 50 MB
	DefaultPageLimit  = 50
	MaxPageLimit      = 100
)

var (
	mentionRegex  = regexp.MustCompile(`@([\w.-]+)`)
	uuidRegex     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	channelSlugRe = regexp.MustCompile(`[^a-z0-9-]+`)
)

func slugifyChannelName(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "_", "-")
	slug = channelSlugRe.ReplaceAllString(slug, "")
	slug = strings.Trim(slug, "-")
	return slug
}

type Service struct {
	pool          *pgxpool.Pool
	hub           *Hub
	storage       documents.Storage
	signingSecret []byte
	logger        *slog.Logger

	authCache sync.Map // key: orgID + ":" + channelID + ":" + employeeID -> bool
}

func NewService(pool *pgxpool.Pool, hub *Hub, storage documents.Storage, secret string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if secret == "" {
		secret = "easyhr-workspace-chat-signing-key-secret-2026"
	}
	s := &Service{
		pool:          pool,
		hub:           hub,
		storage:       storage,
		signingSecret: []byte(secret),
		logger:        logger,
	}
	if hub != nil {
		hub.authorize = func(orgID, channelID, employeeID string, isAdmin bool) bool {
			return s.AuthorizeChannel(context.Background(), orgID, channelID, employeeID, isAdmin)
		}
	}
	return s
}

func (s *Service) Hub() *Hub {
	return s.hub
}

func (s *Service) InvalidateAuthCache(orgID, channelID string) {
	prefix := orgID + ":" + channelID + ":"
	s.authCache.Range(func(key, value any) bool {
		if k, ok := key.(string); ok && strings.HasPrefix(k, prefix) {
			s.authCache.Delete(k)
		}
		return true
	})
}

// AuthorizeChannel verifies if an employee is allowed to participate in or view a channel.
func (s *Service) AuthorizeChannel(ctx context.Context, orgID, channelID, employeeID string, isAdmin bool) bool {
	if orgID == "" || channelID == "" {
		return false
	}
	cacheKey := orgID + ":" + channelID + ":" + employeeID + fmt.Sprintf(":%t", isAdmin)
	if val, ok := s.authCache.Load(cacheKey); ok {
		return val.(bool)
	}

	var realChannelID string
	var isPrivate bool
	var channelType string
	var deptRestricted bool
	var deptName *string
	var chName string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, is_private, channel_type, department_restricted, department_name, name
		FROM chat_channels
		WHERE organization_id = $1::uuid 
		  AND (
		      (CASE WHEN $2 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id = $2::uuid ELSE false END)
		      OR lower(name) = lower($2)
		      OR lower(name) = replace(lower($2), 'ch-', '')
		  )
		LIMIT 1
	`, orgID, channelID).Scan(&realChannelID, &isPrivate, &channelType, &deptRestricted, &deptName, &chName)
	if err != nil {
		return false
	}

	// General and announcements channels are accessible to all workspace employees
	if strings.EqualFold(chName, "general") || strings.EqualFold(chName, "announcements") {
		s.authCache.Store(cacheKey, true)
		return true
	}

	// Admins and Company Owners have access to all non-DM channels (public, department, and private)
	if isAdmin {
		s.authCache.Store(cacheKey, true)
		return true
	}

	// DMs require explicit membership
	if channelType == ChannelTypeDM || channelType == ChannelTypeGroupDM {
		var memberCount int
		_ = s.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM chat_channel_members
			WHERE organization_id = $1::uuid AND channel_id = $2::uuid 
			  AND ($3 = '' OR employee_id::text = $3 OR user_id::text = $3)
		`, orgID, realChannelID, employeeID).Scan(&memberCount)
		allowed := memberCount > 0
		if allowed {
			s.authCache.Store(cacheKey, true)
		}
		return allowed
	}

	// Private channels require explicit membership or matching department
	if isPrivate {
		var memberCount int
		_ = s.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM chat_channel_members
			WHERE organization_id = $1::uuid AND channel_id = $2::uuid 
			  AND ($3 = '' OR employee_id::text = $3 OR user_id::text = $3)
		`, orgID, realChannelID, employeeID).Scan(&memberCount)
		if memberCount > 0 {
			s.authCache.Store(cacheKey, true)
			return true
		}

		if deptRestricted && deptName != nil && *deptName != "" {
			var empDept string
			_ = s.pool.QueryRow(ctx, `
				SELECT COALESCE(d.name, '') 
				FROM employees e
				LEFT JOIN employment_assignments a ON a.employee_id = e.id AND a.effective_to IS NULL
				LEFT JOIN departments d ON d.id = COALESCE(a.department_id, e.department_id)
				WHERE e.organization_id = $1::uuid 
				  AND (e.id::text = $2 OR e.user_id::text = $2)
				LIMIT 1
			`, orgID, employeeID).Scan(&empDept)
			if strings.EqualFold(empDept, *deptName) {
				s.authCache.Store(cacheKey, true)
				return true
			}
		}

		return false
	}

	// All other non-private channels in the workspace (including department channels) are open to all organization members
	s.authCache.Store(cacheKey, true)
	return true
}

func (s *Service) ResolveActor(ctx context.Context, principal identity.Principal) (Actor, error) {
	actor := Actor{
		OrganizationID: principal.OrganizationID,
		UserID:         principal.UserID,
		MembershipID:   principal.MembershipID,
	}

	var firstName, lastName, avatarURL string
	_ = s.pool.QueryRow(ctx, `
		SELECT first_name, last_name, COALESCE(avatar_url, '')
		FROM users WHERE id = $1
	`, principal.UserID).Scan(&firstName, &lastName, &avatarURL)

	var roleKey, roleName string
	_ = s.pool.QueryRow(ctx, `
		SELECT COALESCE(r.key, ''), COALESCE(r.display_name, 'Member')
		FROM membership_roles mr
		JOIN roles r ON r.id = mr.role_id
		WHERE mr.membership_id = $1
		LIMIT 1
	`, principal.MembershipID).Scan(&roleKey, &roleName)
	if roleName == "" {
		roleName = "Member"
	}

	roleLower := strings.ToLower(roleName)
	keyLower := strings.ToLower(roleKey)
	actor.IsAdmin = principal.HasPermission("chat:manage_channels") ||
		principal.HasPermission("organization:manage") ||
		principal.HasPermission("memberships:manage") ||
		principal.HasPermission("employees:manage") ||
		principal.HasPermission("workspace.admin") ||
		principal.HasPermission("people.manage") ||
		principal.HasPermission("organization.owner") ||
		strings.Contains(roleLower, "owner") ||
		strings.Contains(roleLower, "admin") ||
		strings.Contains(roleLower, "hr") ||
		strings.Contains(roleLower, "manager") ||
		strings.Contains(keyLower, "owner") ||
		strings.Contains(keyLower, "admin") ||
		strings.Contains(keyLower, "hr") ||
		strings.Contains(keyLower, "manager")

	actor.FullName = strings.TrimSpace(firstName + " " + lastName)
	if actor.FullName == "" {
		actor.FullName = "Workspace Member"
	}
	actor.RoleTitle = roleName
	actor.AvatarURL = avatarURL

	var empID, empDept, empJobTitle string
	err := s.pool.QueryRow(ctx, `
		SELECT 
			e.id::text, 
			COALESCE(d.name, 'General'), 
			COALESCE(a.job_title, 'Team Member')
		FROM employees e
		LEFT JOIN employment_assignments a ON a.employee_id = e.id AND a.effective_to IS NULL
		LEFT JOIN departments d ON d.id = a.department_id
		WHERE e.organization_id = $1::uuid
		  AND (e.user_id = $2::uuid OR (e.user_id IS NULL AND LOWER(e.work_email) = (SELECT LOWER(email) FROM users WHERE id = $2::uuid)))
		ORDER BY e.created_at DESC
		LIMIT 1
	`, principal.OrganizationID, principal.UserID).Scan(&empID, &empDept, &empJobTitle)

	if err != nil || empID == "" {
		_ = s.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE user_id = $1::uuid LIMIT 1`, principal.UserID).Scan(&empID)
		if empID == "" {
			newEmpID := newUUID()
			email := ""
			_ = s.pool.QueryRow(ctx, "SELECT email FROM users WHERE id = $1::uuid", principal.UserID).Scan(&email)
			if email == "" {
				email = fmt.Sprintf("user-%s@easyhr.local", principal.UserID[:8])
			}
			empNum := "EMP-" + newEmpID[:8]
			_, insertErr := s.pool.Exec(ctx, `
				INSERT INTO employees (id, organization_id, user_id, employee_number, first_name, last_name, work_email, status, hired_on)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7, 'active', CURRENT_DATE)
				ON CONFLICT DO NOTHING
			`, newEmpID, principal.OrganizationID, principal.UserID, empNum, firstName, lastName, email)
			if insertErr != nil {
				uniqueEmail := fmt.Sprintf("%s+%s@easyhr.local", email, newEmpID[:6])
				_, _ = s.pool.Exec(ctx, `
					INSERT INTO employees (id, organization_id, employee_number, first_name, last_name, work_email, status, hired_on)
					VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, 'active', CURRENT_DATE)
					ON CONFLICT DO NOTHING
				`, newEmpID, principal.OrganizationID, empNum, firstName, lastName, uniqueEmail)
			}
			_ = s.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE id = $1::uuid`, newEmpID).Scan(&empID)
			if empID == "" {
				_ = s.pool.QueryRow(ctx, `SELECT id::text FROM employees WHERE organization_id = $1::uuid LIMIT 1`, principal.OrganizationID).Scan(&empID)
			}
			empDept = "General"
			empJobTitle = roleName
		}
	} else if principal.UserID != "" {
		_, _ = s.pool.Exec(ctx, "UPDATE employees SET user_id = $1::uuid WHERE id = $2::uuid AND user_id IS NULL", principal.UserID, empID)
	}

	actor.EmployeeID = empID
	if empDept != "" {
		actor.Department = empDept
	}
	if empJobTitle != "" {
		actor.RoleTitle = empJobTitle
	}

	return actor, nil
}

func (s *Service) EnsureDefaultChannels(ctx context.Context, orgID, creatorID string) error {
	if orgID == "" {
		return nil
	}

	// 1. Fetch all employees in this organization
	rows, err := s.pool.Query(ctx, `SELECT id::text, user_id::text FROM employees WHERE organization_id = $1::uuid`, orgID)
	if err != nil {
		return err
	}
	type empRef struct {
		empID  string
		userID *string
	}
	var emps []empRef
	for rows.Next() {
		var e empRef
		if err := rows.Scan(&e.empID, &e.userID); err == nil {
			emps = append(emps, e)
		}
	}
	rows.Close()

	// 2. Fetch admin / HR / owner employees
	adminEmpIDs := make(map[string]bool)
	adminRows, err := s.pool.Query(ctx, `
		SELECT DISTINCT e.id::text
		FROM employees e
		JOIN memberships m ON m.organization_id = e.organization_id AND (m.user_id = e.user_id OR LOWER(m.user_id::text) = LOWER(e.user_id::text))
		JOIN membership_roles mr ON mr.membership_id = m.id
		JOIN roles r ON r.id = mr.role_id
		WHERE e.organization_id = $1::uuid
		  AND (
		      r.key IN ('company_owner', 'admin', 'hr_manager', 'hr_admin', 'workspace_admin')
		      OR r.display_name ILIKE '%owner%'
		      OR r.display_name ILIKE '%admin%'
		      OR r.display_name ILIKE '%hr%'
		      OR r.display_name ILIKE '%manager%'
		  )
	`, orgID)
	if err == nil {
		for adminRows.Next() {
			var aID string
			if err := adminRows.Scan(&aID); err == nil && aID != "" {
				adminEmpIDs[aID] = true
			}
		}
		adminRows.Close()
	}

	// 3. Ensure #general channel exists
	var genID string
	err = s.pool.QueryRow(ctx, `
		SELECT id::text FROM chat_channels 
		WHERE organization_id = $1::uuid AND lower(name) = 'general'
		LIMIT 1
	`, orgID).Scan(&genID)
	if err != nil || genID == "" {
		genID = newUUID()
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO chat_channels (
				id, organization_id, name, channel_type, topic, description,
				is_private, department_restricted, department_name, created_by_name
			) VALUES (
				$1::uuid, $2::uuid, 'general', 'channel', 
				'Welcome to EasyHR Workspace Chat!', 
				'Company-wide announcements and general discussion',
				false, false, NULL, 'EasyHR System'
			) ON CONFLICT DO NOTHING
		`, genID, orgID)
	}

	// 4. Ensure #announcements channel exists
	var annID string
	err = s.pool.QueryRow(ctx, `
		SELECT id::text FROM chat_channels 
		WHERE organization_id = $1::uuid AND lower(name) = 'announcements'
		LIMIT 1
	`, orgID).Scan(&annID)
	if err != nil || annID == "" {
		annID = newUUID()
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO chat_channels (
				id, organization_id, name, channel_type, topic, description,
				is_private, department_restricted, department_name, created_by_name
			) VALUES (
				$1::uuid, $2::uuid, 'announcements', 'channel', 
				'Official updates from HR and leadership', 
				'Official updates from HR and leadership. Only HR and Admins can post.',
				false, false, NULL, 'EasyHR System'
			) ON CONFLICT DO NOTHING
		`, annID, orgID)
	}

	// Add all employees to #general and #announcements
	for _, chID := range []string{genID, annID} {
		if chID != "" {
			for _, e := range emps {
				role := "member"
				if adminEmpIDs[e.empID] {
					role = "admin"
				}
				_, _ = s.pool.Exec(ctx, `
					INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role)
					VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, $5)
					ON CONFLICT DO NOTHING
				`, chID, orgID, e.empID, e.userID, role)
			}
		}
	}

	// 5. Dynamic Department Channels
	deptRows, err := s.pool.Query(ctx, `
		SELECT id::text, name 
		FROM departments 
		WHERE organization_id = $1::uuid AND status = 'active'
		ORDER BY name ASC
	`, orgID)
	if err == nil {
		type deptInfo struct {
			id   string
			name string
		}
		var depts []deptInfo
		for deptRows.Next() {
			var d deptInfo
			if err := deptRows.Scan(&d.id, &d.name); err == nil && d.name != "" {
				depts = append(depts, d)
			}
		}
		deptRows.Close()

		for _, d := range depts {
			slug := slugifyChannelName(d.name)
			if slug == "" {
				slug = "dept-" + d.id[:8]
			}

			var chID string
			err := s.pool.QueryRow(ctx, `
				SELECT id::text FROM chat_channels
				WHERE organization_id = $1::uuid AND (lower(name) = lower($2) OR department_name = $3)
				LIMIT 1
			`, orgID, slug, d.name).Scan(&chID)

			if err != nil || chID == "" {
				chID = newUUID()
				_, _ = s.pool.Exec(ctx, `
					INSERT INTO chat_channels (
						id, organization_id, name, channel_type, topic, description,
						is_private, department_restricted, department_name, created_by_name
					) VALUES (
						$1::uuid, $2::uuid, $3, 'channel',
						$4, $5,
						false, true, $6, 'EasyHR System'
					) ON CONFLICT DO NOTHING
				`, chID, orgID, slug,
					fmt.Sprintf("%s department channel", d.name),
					fmt.Sprintf("Discussions, updates, and collaboration for the %s team", d.name),
					d.name)
			}

			// Add department employees
			depEmps, err := s.pool.Query(ctx, `
				SELECT e.id::text, e.user_id::text
				FROM employees e
				LEFT JOIN employment_assignments a ON a.employee_id = e.id AND a.effective_to IS NULL
				WHERE e.organization_id = $1::uuid 
				  AND (a.department_id = $2::uuid)
			`, orgID, d.id)
			if err == nil {
				for depEmps.Next() {
					var e empRef
					if err := depEmps.Scan(&e.empID, &e.userID); err == nil {
						_, _ = s.pool.Exec(ctx, `
							INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role)
							VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, 'member')
							ON CONFLICT DO NOTHING
						`, chID, orgID, e.empID, e.userID)
					}
				}
				depEmps.Close()
			}

			// Also ensure HR Admins and Company Owners are added to each department channel
			for _, e := range emps {
				if adminEmpIDs[e.empID] {
					_, _ = s.pool.Exec(ctx, `
						INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role)
						VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, 'admin')
						ON CONFLICT DO NOTHING
					`, chID, orgID, e.empID, e.userID)
				}
			}
		}
	}

	return nil
}

func (s *Service) ListChannels(ctx context.Context, actor Actor) ([]Channel, error) {
	_ = s.EnsureDefaultChannels(ctx, actor.OrganizationID, actor.EmployeeID)

	query := `
		WITH member_stats AS (
			SELECT 
				channel_id,
				is_muted,
				last_read_message_id,
				last_read_at
			FROM chat_channel_members
			WHERE organization_id = $1::uuid AND employee_id = $2::uuid
		),
		channel_members_agg AS (
			SELECT 
				channel_id,
				array_agg(employee_id::text) AS member_ids
			FROM chat_channel_members
			WHERE organization_id = $1::uuid
			GROUP BY channel_id
		),
		pending_requests_agg AS (
			SELECT 
				channel_id,
				array_agg(employee_id::text) AS pending_ids
			FROM chat_channel_join_requests
			WHERE organization_id = $1::uuid AND status = 'pending'
			GROUP BY channel_id
		),
		last_messages AS (
			SELECT 
				channel_id,
				MAX(created_at) AS last_msg_at
			FROM chat_messages
			WHERE organization_id = $1::uuid AND deleted_at IS NULL
			GROUP BY channel_id
		),
		unread_counts AS (
			SELECT 
				m.channel_id,
				COUNT(msg.id) AS unread_count
			FROM member_stats m
			JOIN chat_messages msg ON msg.channel_id = m.channel_id AND msg.organization_id = $1::uuid AND msg.deleted_at IS NULL
			WHERE (m.last_read_at IS NULL OR msg.created_at > m.last_read_at)
			  AND msg.sender_employee_id != $2::uuid
			GROUP BY m.channel_id
		)
		SELECT 
			c.id::text,
			c.name,
			c.description,
			c.topic,
			c.is_private,
			c.channel_type,
			COALESCE(c.department_name, ''),
			COALESCE(c.created_by_name, 'System'),
			c.is_archived,
			c.created_at,
			c.updated_at,
			COALESCE(lm.last_msg_at, c.created_at),
			COALESCE(ms.is_muted, false),
			COALESCE(ms.last_read_message_id::text, ''),
			COALESCE(ms.last_read_at, '1970-01-01'::timestamptz),
			COALESCE(uc.unread_count, 0),
			COALESCE(cma.member_ids, ARRAY[]::text[]),
			COALESCE(pra.pending_ids, ARRAY[]::text[])
		FROM chat_channels c
		LEFT JOIN member_stats ms ON ms.channel_id = c.id
		LEFT JOIN channel_members_agg cma ON cma.channel_id = c.id
		LEFT JOIN pending_requests_agg pra ON pra.channel_id = c.id
		LEFT JOIN last_messages lm ON lm.channel_id = c.id
		LEFT JOIN unread_counts uc ON uc.channel_id = c.id
		WHERE c.organization_id = $1::uuid
		  AND (
		      c.is_private = false 
		      OR ms.channel_id IS NOT NULL 
		      OR $3 = true 
		      OR c.created_by_employee_id = $2::uuid
		  )
		ORDER BY c.is_archived ASC, lm.last_msg_at DESC NULLS LAST, c.name ASC
	`

	rows, err := s.pool.Query(ctx, query, actor.OrganizationID, actor.EmployeeID, actor.IsAdmin)
	if err != nil {
		return nil, fmt.Errorf("list channels failed: %w", err)
	}
	defer rows.Close()

	var channels []Channel
	for rows.Next() {
		var ch Channel
		var createdAt, updatedAt, lastMsgAt, lastReadAt time.Time
		var lastReadMsgID string

		err := rows.Scan(
			&ch.ID, &ch.Name, &ch.Description, &ch.Topic,
			&ch.IsPrivate, &ch.ChannelType, &ch.DepartmentRestriction,
			&ch.CreatedBy, &ch.IsArchived, &createdAt, &updatedAt,
			&lastMsgAt, &ch.IsMuted, &lastReadMsgID, &lastReadAt,
			&ch.UnreadCount, &ch.MemberIDs, &ch.PendingRequestUserIDs,
		)
		if err != nil {
			return nil, fmt.Errorf("scan channel failed: %w", err)
		}

		ch.OrganizationID = actor.OrganizationID
		ch.CreatedAt = createdAt.Format(time.RFC3339)
		ch.UpdatedAt = updatedAt.Format(time.RFC3339)
		ch.LastMessageAt = lastMsgAt.Format(time.RFC3339)
		if lastReadMsgID != "" {
			ch.LastReadMessageID = lastReadMsgID
			ch.LastReadAt = lastReadAt.Format(time.RFC3339)
		}
		if ch.MemberIDs == nil {
			ch.MemberIDs = []string{}
		}
		if ch.PendingRequestUserIDs == nil {
			ch.PendingRequestUserIDs = []string{}
		}

		channels = append(channels, ch)
	}

	return channels, nil
}

func (s *Service) CreateChannel(ctx context.Context, actor Actor, in CreateChannelInput) (Channel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return Channel{}, fmt.Errorf("%w: channel name cannot be empty", ErrInvalidInput)
	}
	name = strings.TrimPrefix(name, "#")

	channelType := in.ChannelType
	if channelType == "" {
		channelType = "channel"
	}

	chID := newUUID()
	now := time.Now().UTC()
	deptRestricted := in.DepartmentRestriction != ""

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback(ctx)

	var validActorUserID, validActorEmpID string
	if uuidRegex.MatchString(actor.UserID) {
		validActorUserID = actor.UserID
	}
	if uuidRegex.MatchString(actor.EmployeeID) {
		validActorEmpID = actor.EmployeeID
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO chat_channels (
			id, organization_id, name, channel_type, topic, description,
			is_private, department_restricted, department_name,
			created_by_user_id, created_by_employee_id, created_by_name,
			created_at, updated_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, $5, $6,
			$7, $8, NULLIF($9, ''),
			NULLIF($10, '')::uuid, NULLIF($11, '')::uuid, $12,
			$13, $13
		)
	`, chID, actor.OrganizationID, name, channelType, in.Topic, in.Description,
		in.IsPrivate, deptRestricted, in.DepartmentRestriction,
		validActorUserID, validActorEmpID, actor.FullName, now)
	if err != nil {
		return Channel{}, fmt.Errorf("create channel failed: %w", err)
	}

	membersToInsert := make(map[string]bool)
	if validActorEmpID != "" {
		membersToInsert[validActorEmpID] = true
	}
	for _, mid := range in.MemberIDs {
		mid = strings.TrimSpace(mid)
		if mid != "" && uuidRegex.MatchString(mid) {
			membersToInsert[mid] = true
		}
	}

	memberIDsSlice := make([]string, 0, len(membersToInsert))
	for mid := range membersToInsert {
		role := "member"
		if mid == validActorEmpID {
			role = "admin"
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role, joined_at)
			VALUES (
				$1::uuid, $2::uuid, $3::uuid, 
				(SELECT user_id FROM employees WHERE id = $3::uuid),
				$4, $5
			)
			ON CONFLICT DO NOTHING
		`, chID, actor.OrganizationID, mid, role, now)
		memberIDsSlice = append(memberIDsSlice, mid)
	}

	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}

	s.InvalidateAuthCache(actor.OrganizationID, chID)

	ch := Channel{
		ID:                    chID,
		OrganizationID:        actor.OrganizationID,
		Name:                  name,
		Description:           in.Description,
		Topic:                 in.Topic,
		IsPrivate:             in.IsPrivate,
		ChannelType:           channelType,
		DepartmentRestriction: in.DepartmentRestriction,
		UnreadCount:           0,
		LastMessageAt:         now.Format(time.RFC3339),
		MemberIDs:             memberIDsSlice,
		PendingRequestUserIDs: []string{},
		CreatedBy:             actor.FullName,
		CreatedAt:             now.Format(time.RFC3339),
		UpdatedAt:             now.Format(time.RFC3339),
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelCreated,
		OrganizationID: actor.OrganizationID,
		ChannelID:      chID,
		ActorID:        actor.EmployeeID,
		Payload:        ch,
		Timestamp:      now.Format(time.RFC3339),
	})

	return ch, nil
}

func (s *Service) GetOrCreateDM(ctx context.Context, actor Actor, in CreateDMInput) (Channel, error) {
	participants := map[string]bool{actor.EmployeeID: true}
	for _, pid := range in.ParticipantIDs {
		pid = strings.TrimSpace(pid)
		if pid != "" {
			participants[pid] = true
		}
	}

	pList := make([]string, 0, len(participants))
	for pid := range participants {
		pList = append(pList, pid)
	}

	if len(pList) < 2 {
		return Channel{}, fmt.Errorf("%w: a direct message requires at least one other participant", ErrInvalidInput)
	}

	channelType := "dm"
	if len(pList) > 2 {
		channelType = "group_dm"
	}

	// Compute canonical dm_key
	sortCopy := make([]string, len(pList))
	copy(sortCopy, pList)
	for i := 0; i < len(sortCopy); i++ {
		for j := i + 1; j < len(sortCopy); j++ {
			if sortCopy[i] > sortCopy[j] {
				sortCopy[i], sortCopy[j] = sortCopy[j], sortCopy[i]
			}
		}
	}
	dmKey := strings.Join(sortCopy, ":")

	var existingID string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM chat_channels
		WHERE organization_id = $1::uuid AND dm_key = $2
	`, actor.OrganizationID, dmKey).Scan(&existingID)
	if err == nil && existingID != "" {
		channels, err := s.ListChannels(ctx, actor)
		if err == nil {
			for _, ch := range channels {
				if ch.ID == existingID {
					return ch, nil
				}
			}
		}
	}

	dmName := in.Name
	if dmName == "" {
		var names []string
		rows, err := s.pool.Query(ctx, `
			SELECT first_name || ' ' || last_name
			FROM employees
			WHERE organization_id = $1::uuid AND id = ANY($2::uuid[])
		`, actor.OrganizationID, pList)
		if err == nil {
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err == nil && n != "" {
					names = append(names, n)
				}
			}
			rows.Close()
		}
		if len(names) > 0 {
			dmName = strings.Join(names, ", ")
		} else {
			dmName = "Direct Message"
		}
	}

	chID := newUUID()
	now := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO chat_channels (
			id, organization_id, name, channel_type, topic, description,
			is_private, department_restricted, dm_key,
			created_by_user_id, created_by_employee_id, created_by_name,
			created_at, updated_at
		) VALUES (
			$1::uuid, $2::uuid, $3, $4, 'Direct Message', 'Direct conversation',
			true, false, $5,
			NULLIF($6, '')::uuid, NULLIF($7, '')::uuid, $8,
			$9, $9
		)
	`, chID, actor.OrganizationID, dmName, channelType, dmKey,
		actor.UserID, actor.EmployeeID, actor.FullName, now)
	if err != nil {
		return Channel{}, err
	}

	for _, pid := range pList {
		_, _ = tx.Exec(ctx, `
			INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role, joined_at)
			VALUES (
				$1::uuid, $2::uuid, $3::uuid,
				(SELECT user_id FROM employees WHERE id = $3::uuid),
				'member', $4
			) ON CONFLICT DO NOTHING
		`, chID, actor.OrganizationID, pid, now)
	}

	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}

	s.InvalidateAuthCache(actor.OrganizationID, chID)

	ch := Channel{
		ID:             chID,
		OrganizationID: actor.OrganizationID,
		Name:           dmName,
		Description:    "Direct conversation",
		Topic:          "Direct Message",
		IsPrivate:      true,
		ChannelType:    channelType,
		UnreadCount:    0,
		LastMessageAt:  now.Format(time.RFC3339),
		MemberIDs:      pList,
		CreatedBy:      actor.FullName,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}

	return ch, nil
}

func (s *Service) UpdateChannel(ctx context.Context, actor Actor, channelID string, in UpdateChannelInput) (Channel, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return Channel{}, ErrForbidden
	}

	now := time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE chat_channels
		SET 
			name = COALESCE($1, name),
			description = COALESCE($2, description),
			topic = COALESCE($3, topic),
			is_private = COALESCE($4, is_private),
			updated_at = $5
		WHERE organization_id = $6::uuid AND id = $7::uuid
	`, in.Name, in.Description, in.Topic, in.IsPrivate, now, actor.OrganizationID, channelID)
	if err != nil {
		return Channel{}, err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	channels, err := s.ListChannels(ctx, actor)
	if err != nil {
		return Channel{}, err
	}
	for _, ch := range channels {
		if ch.ID == channelID {
			s.hub.Publish(RealtimeEvent{
				Version:        EventVersionV1,
				Type:           EventChannelUpdated,
				OrganizationID: actor.OrganizationID,
				ChannelID:      channelID,
				ActorID:        actor.EmployeeID,
				Payload:        ch,
				Timestamp:      now.Format(time.RFC3339),
			})
			return ch, nil
		}
	}
	return Channel{}, ErrNotFound
}

func (s *Service) ArchiveChannel(ctx context.Context, actor Actor, channelID string) error {
	if !actor.IsAdmin {
		var createdBy *string
		_ = s.pool.QueryRow(ctx, `SELECT created_by_employee_id::text FROM chat_channels WHERE organization_id = $1::uuid AND id = $2::uuid`, actor.OrganizationID, channelID).Scan(&createdBy)
		if createdBy == nil || *createdBy != actor.EmployeeID {
			return ErrForbidden
		}
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE chat_channels
		SET is_archived = true, updated_at = NOW()
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, channelID)
	if err != nil {
		return err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelArchived,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload:        map[string]any{"channelId": channelID, "isArchived": true},
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) DeleteChannel(ctx context.Context, actor Actor, channelID string) error {
	if !actor.IsAdmin {
		var createdBy *string
		_ = s.pool.QueryRow(ctx, `SELECT created_by_employee_id::text FROM chat_channels WHERE organization_id = $1::uuid AND id = $2::uuid`, actor.OrganizationID, channelID).Scan(&createdBy)
		if createdBy == nil || *createdBy != actor.EmployeeID {
			return ErrForbidden
		}
	}

	_, err := s.pool.Exec(ctx, `DELETE FROM chat_channels WHERE organization_id = $1::uuid AND id = $2::uuid`, actor.OrganizationID, channelID)
	if err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelDeleted,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload:        map[string]any{"channelId": channelID},
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) JoinChannel(ctx context.Context, actor Actor, channelID string) error {
	var isPrivate bool
	err := s.pool.QueryRow(ctx, `SELECT is_private FROM chat_channels WHERE organization_id = $1::uuid AND id = $2::uuid`, actor.OrganizationID, channelID).Scan(&isPrivate)
	if err != nil {
		return ErrNotFound
	}
	if isPrivate && !actor.IsAdmin {
		return ErrForbidden
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role, joined_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, 'member', NOW())
		ON CONFLICT DO NOTHING
	`, channelID, actor.OrganizationID, actor.EmployeeID, actor.UserID)
	if err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelMemberJoined,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": actor.EmployeeID,
			"userName":   actor.FullName,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) LeaveChannel(ctx context.Context, actor Actor, channelID string) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM chat_channel_members
		WHERE organization_id = $1::uuid AND channel_id = $2::uuid AND employee_id = $3::uuid
	`, actor.OrganizationID, channelID, actor.EmployeeID)
	if err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelMemberLeft,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": actor.EmployeeID,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) RequestToJoinChannel(ctx context.Context, actor Actor, channelID string) error {
	var isPrivate bool
	var createdBy *string
	err := s.pool.QueryRow(ctx, `SELECT is_private, created_by_employee_id::text FROM chat_channels WHERE organization_id = $1::uuid AND id = $2::uuid`, actor.OrganizationID, channelID).Scan(&isPrivate, &createdBy)
	if err != nil {
		return ErrNotFound
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO chat_channel_join_requests (channel_id, organization_id, employee_id, user_id, status, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, 'pending', NOW())
		ON CONFLICT (channel_id, employee_id) DO UPDATE SET status = 'pending', updated_at = NOW()
	`, channelID, actor.OrganizationID, actor.EmployeeID, actor.UserID)
	if err != nil {
		return err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelJoinRequested,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": actor.EmployeeID,
			"userName":   actor.FullName,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	go func() {
		if createdBy != nil && *createdBy != "" {
			var creatorUserID *string
			_ = s.pool.QueryRow(context.Background(), `
				SELECT user_id::text FROM employees WHERE organization_id = $1::uuid AND id = $2::uuid
			`, actor.OrganizationID, *createdBy).Scan(&creatorUserID)

			if creatorUserID != nil && *creatorUserID != "" {
				_, _ = s.pool.Exec(context.Background(), `
					INSERT INTO user_notifications (organization_id, user_id, title, message, category, link)
					VALUES ($1::uuid, $2::uuid, $3, $4, 'chat', '/chat')
				`, actor.OrganizationID, *creatorUserID, "Channel join request", fmt.Sprintf("%s requested to join a private channel", actor.FullName))
			}
		}
	}()

	return nil
}

func (s *Service) ReviewJoinRequest(ctx context.Context, actor Actor, channelID, requesterEmployeeID string, approved bool) error {
	status := "rejected"
	if approved {
		status = "approved"
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE chat_channel_join_requests
		SET status = $1, reviewed_by_user_id = NULLIF($2, '')::uuid, updated_at = NOW()
		WHERE organization_id = $3::uuid AND channel_id = $4::uuid AND employee_id = $5::uuid
	`, status, actor.UserID, actor.OrganizationID, channelID, requesterEmployeeID)
	if err != nil {
		return err
	}

	if approved {
		_, _ = tx.Exec(ctx, `
			INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role, joined_at)
			VALUES (
				$1::uuid, $2::uuid, $3::uuid,
				(SELECT user_id FROM employees WHERE id = $3::uuid),
				'member', NOW()
			)
			ON CONFLICT DO NOTHING
		`, channelID, actor.OrganizationID, requesterEmployeeID)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelJoinReviewed,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": requesterEmployeeID,
			"approved":   approved,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

func (s *Service) AddMember(ctx context.Context, actor Actor, channelID, targetEmployeeID string) error {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return ErrForbidden
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, role, joined_at)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid,
			(SELECT user_id FROM employees WHERE id = $3::uuid),
			'member', NOW()
		)
		ON CONFLICT DO NOTHING
	`, channelID, actor.OrganizationID, targetEmployeeID)
	if err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelMemberJoined,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": targetEmployeeID,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) RemoveMember(ctx context.Context, actor Actor, channelID, targetEmployeeID string) error {
	if !actor.IsAdmin && actor.EmployeeID != targetEmployeeID {
		return ErrForbidden
	}

	_, err := s.pool.Exec(ctx, `
		DELETE FROM chat_channel_members
		WHERE organization_id = $1::uuid AND channel_id = $2::uuid AND employee_id = $3::uuid
	`, actor.OrganizationID, channelID, targetEmployeeID)
	if err != nil {
		return err
	}

	s.InvalidateAuthCache(actor.OrganizationID, channelID)

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventChannelMemberLeft,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":  channelID,
			"employeeId": targetEmployeeID,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
	return nil
}

func (s *Service) MarkChannelRead(ctx context.Context, actor Actor, channelID, lastMessageID string) error {
	now := time.Now().UTC()
	var msgCreatedAt time.Time
	if lastMessageID != "" {
		_ = s.pool.QueryRow(ctx, `
			SELECT created_at FROM chat_messages
			WHERE organization_id = $1::uuid AND id = $2::uuid
		`, actor.OrganizationID, lastMessageID).Scan(&msgCreatedAt)
	}
	if msgCreatedAt.IsZero() {
		msgCreatedAt = now
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, last_read_message_id, last_read_at, unread_count)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6, 0)
		ON CONFLICT (channel_id, employee_id)
		DO UPDATE SET last_read_message_id = EXCLUDED.last_read_message_id, last_read_at = EXCLUDED.last_read_at, unread_count = 0
	`, channelID, actor.OrganizationID, actor.EmployeeID, actor.UserID, lastMessageID, msgCreatedAt)
	if err != nil {
		return err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessageRead,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"channelId":         channelID,
			"employeeId":        actor.EmployeeID,
			"lastReadMessageId": lastMessageID,
			"lastReadAt":        msgCreatedAt.Format(time.RFC3339),
		},
		Timestamp: now.Format(time.RFC3339),
	})
	return nil
}

func (s *Service) UpdateChannelPreferences(ctx context.Context, actor Actor, channelID string, isMuted bool) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, is_muted)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, $5)
		ON CONFLICT (channel_id, employee_id) DO UPDATE SET is_muted = EXCLUDED.is_muted
	`, channelID, actor.OrganizationID, actor.EmployeeID, actor.UserID, isMuted)
	return err
}

func (s *Service) ListMessages(ctx context.Context, actor Actor, filter ListMessagesFilter) (MessagePage, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, filter.ChannelID, actor.EmployeeID, actor.IsAdmin) {
		return MessagePage{}, ErrForbidden
	}

	var realChannelID string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM chat_channels
		WHERE organization_id = $1::uuid 
		  AND (
		      (CASE WHEN $2 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id = $2::uuid ELSE false END)
		      OR lower(name) = lower($2)
		      OR lower(name) = replace(lower($2), 'ch-', '')
		  )
		LIMIT 1
	`, actor.OrganizationID, filter.ChannelID).Scan(&realChannelID)
	if err == nil && realChannelID != "" {
		filter.ChannelID = realChannelID
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	} else if limit > MaxPageLimit {
		limit = MaxPageLimit
	}

	var sb strings.Builder
	args := []any{actor.OrganizationID, filter.ChannelID, actor.EmployeeID}
	argIdx := 4

	sb.WriteString(`
		SELECT 
			m.id::text,
			m.channel_id::text,
			COALESCE(m.sender_employee_id::text, m.sender_user_id::text, ''),
			m.sender_name,
			COALESCE(NULLIF(m.sender_avatar, ''), u.avatar_url, ''),
			m.sender_role,
			m.content,
			m.created_at,
			m.edited_at,
			(m.deleted_at IS NOT NULL),
			COALESCE(m.client_msg_id, ''),
			COALESCE(m.parent_message_id::text, ''),
			m.reply_count,
			m.is_pinned,
			(s.message_id IS NOT NULL) AS is_saved
		FROM chat_messages m
		LEFT JOIN users u ON u.id = COALESCE(m.sender_user_id, (SELECT user_id FROM employees WHERE id = m.sender_employee_id))
		LEFT JOIN chat_message_saves s ON s.message_id = m.id AND s.employee_id = (
			CASE WHEN $3 <> '' AND $3 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $3::uuid ELSE NULL END
		)
		WHERE m.organization_id = $1::uuid 
		  AND m.channel_id = $2::uuid
	`)

	if !filter.IncludeThreads {
		sb.WriteString(" AND m.parent_message_id IS NULL ")
	}
	if filter.OnlyPinned {
		sb.WriteString(" AND m.is_pinned = true ")
	}
	if filter.OnlySaved {
		sb.WriteString(" AND s.message_id IS NOT NULL ")
	}

	if filter.BeforeCursor != "" {
		sb.WriteString(fmt.Sprintf(" AND m.created_at < (SELECT created_at FROM chat_messages WHERE id = $%d::uuid) ", argIdx))
		args = append(args, filter.BeforeCursor)
		argIdx++
	} else if filter.AfterCursor != "" {
		sb.WriteString(fmt.Sprintf(" AND m.created_at > (SELECT created_at FROM chat_messages WHERE id = $%d::uuid) ", argIdx))
		args = append(args, filter.AfterCursor)
		argIdx++
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY m.created_at DESC LIMIT $%d ", argIdx))
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("list messages query failed: %w", err)
	}
	defer rows.Close()

	var descMessages []Message
	for rows.Next() {
		var msg Message
		var createdAt time.Time
		var editedAt *time.Time

		err := rows.Scan(
			&msg.ID, &msg.ChannelID, &msg.SenderID, &msg.SenderName,
			&msg.SenderAvatar, &msg.SenderRole, &msg.Content,
			&createdAt, &editedAt, &msg.IsDeleted, &msg.ClientMsgID,
			&msg.ReplyToID, &msg.ThreadCount, &msg.Pinned, &msg.Saved,
		)
		if err != nil {
			return MessagePage{}, fmt.Errorf("scan message failed: %w", err)
		}

		msg.Timestamp = createdAt.Format(time.RFC3339)
		if editedAt != nil {
			msg.EditedAt = editedAt.Format(time.RFC3339)
		}
		msg.Reactions = make(map[string][]string)

		descMessages = append(descMessages, msg)
	}

	hasMore := len(descMessages) > limit
	if hasMore {
		descMessages = descMessages[:limit]
	}

	var nextCursor string
	if len(descMessages) > 0 {
		nextCursor = descMessages[len(descMessages)-1].ID
	}

	// Reverse to ascending chronological order
	chronological := make([]Message, len(descMessages))
	for i, m := range descMessages {
		chronological[len(descMessages)-1-i] = m
	}

	if len(chronological) > 0 {
		_ = s.batchLoadMessageRelations(ctx, actor.OrganizationID, chronological)
	}

	return MessagePage{
		Messages:   chronological,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) GetThreadReplies(ctx context.Context, actor Actor, channelID, parentMessageID string) ([]Message, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return nil, ErrForbidden
	}

	var realChannelID string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM chat_channels
		WHERE organization_id = $1::uuid 
		  AND (
		      (CASE WHEN $2 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id = $2::uuid ELSE false END)
		      OR lower(name) = lower($2)
		      OR lower(name) = replace(lower($2), 'ch-', '')
		  )
		LIMIT 1
	`, actor.OrganizationID, channelID).Scan(&realChannelID)
	if err == nil && realChannelID != "" {
		channelID = realChannelID
	}

	query := `
		SELECT 
			m.id::text,
			m.channel_id::text,
			COALESCE(m.sender_employee_id::text, m.sender_user_id::text, ''),
			m.sender_name,
			COALESCE(NULLIF(m.sender_avatar, ''), u.avatar_url, ''),
			m.sender_role,
			m.content,
			m.created_at,
			m.edited_at,
			(m.deleted_at IS NOT NULL),
			COALESCE(m.client_msg_id, ''),
			COALESCE(m.parent_message_id::text, ''),
			0 AS thread_count,
			m.is_pinned,
			(s.message_id IS NOT NULL) AS is_saved
		FROM chat_messages m
		LEFT JOIN users u ON u.id = COALESCE(m.sender_user_id, (SELECT user_id FROM employees WHERE id = m.sender_employee_id))
		LEFT JOIN chat_message_saves s ON s.message_id = m.id AND s.employee_id = (
			CASE WHEN $3 <> '' AND $3 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $3::uuid ELSE NULL END
		)
		WHERE m.organization_id = $1::uuid 
		  AND m.channel_id = $2::uuid
		  AND m.parent_message_id = $4::uuid
		ORDER BY m.created_at ASC
	`
	rows, err := s.pool.Query(ctx, query, actor.OrganizationID, channelID, actor.EmployeeID, parentMessageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var replies []Message
	for rows.Next() {
		var msg Message
		var createdAt time.Time
		var editedAt *time.Time

		err := rows.Scan(
			&msg.ID, &msg.ChannelID, &msg.SenderID, &msg.SenderName,
			&msg.SenderAvatar, &msg.SenderRole, &msg.Content,
			&createdAt, &editedAt, &msg.IsDeleted, &msg.ClientMsgID,
			&msg.ReplyToID, &msg.ThreadCount, &msg.Pinned, &msg.Saved,
		)
		if err != nil {
			return nil, err
		}
		msg.Timestamp = createdAt.Format(time.RFC3339)
		if editedAt != nil {
			msg.EditedAt = editedAt.Format(time.RFC3339)
		}
		msg.Reactions = make(map[string][]string)
		replies = append(replies, msg)
	}

	if len(replies) > 0 {
		_ = s.batchLoadMessageRelations(ctx, actor.OrganizationID, replies)
	}

	return replies, nil
}

func (s *Service) SearchMessages(ctx context.Context, actor Actor, in SearchMessagesInput) (MessagePage, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultPageLimit
	} else if limit > MaxPageLimit {
		limit = MaxPageLimit
	}

	var sb strings.Builder
	args := []any{actor.OrganizationID, actor.EmployeeID, actor.IsAdmin}
	argIdx := 4

	sb.WriteString(`
		SELECT 
			m.id::text,
			m.channel_id::text,
			COALESCE(m.sender_employee_id::text, m.sender_user_id::text, ''),
			m.sender_name,
			m.sender_avatar,
			m.sender_role,
			m.content,
			m.created_at,
			m.edited_at,
			(m.deleted_at IS NOT NULL),
			COALESCE(m.client_msg_id, ''),
			COALESCE(m.parent_message_id::text, ''),
			m.reply_count,
			m.is_pinned,
			(s.message_id IS NOT NULL) AS is_saved
		FROM chat_messages m
		JOIN chat_channels c ON c.id = m.channel_id
		LEFT JOIN chat_channel_members cm ON cm.channel_id = c.id AND cm.employee_id = $2::uuid
		LEFT JOIN chat_message_saves s ON s.message_id = m.id AND s.employee_id = $2::uuid
		WHERE m.organization_id = $1::uuid 
		  AND m.deleted_at IS NULL
		  AND (c.is_private = false OR cm.employee_id IS NOT NULL OR $3 = true)
	`)

	if in.ChannelID != "" {
		sb.WriteString(fmt.Sprintf(" AND m.channel_id = $%d::uuid ", argIdx))
		args = append(args, in.ChannelID)
		argIdx++
	}
	if in.SenderID != "" {
		sb.WriteString(fmt.Sprintf(" AND (m.sender_employee_id = $%d::uuid OR m.sender_user_id = $%d::uuid) ", argIdx, argIdx))
		args = append(args, in.SenderID)
		argIdx++
	}
	if in.Query != "" {
		sb.WriteString(fmt.Sprintf(" AND m.search_tsv @@ plainto_tsquery('english', $%d) ", argIdx))
		args = append(args, in.Query)
		argIdx++
	}
	if in.FromDate != nil {
		sb.WriteString(fmt.Sprintf(" AND m.created_at >= $%d ", argIdx))
		args = append(args, *in.FromDate)
		argIdx++
	}
	if in.ToDate != nil {
		sb.WriteString(fmt.Sprintf(" AND m.created_at <= $%d ", argIdx))
		args = append(args, *in.ToDate)
		argIdx++
	}
	if in.OnlyPinned {
		sb.WriteString(" AND m.is_pinned = true ")
	}
	if in.OnlySaved {
		sb.WriteString(" AND s.message_id IS NOT NULL ")
	}

	if in.Cursor != "" {
		sb.WriteString(fmt.Sprintf(" AND m.created_at < (SELECT created_at FROM chat_messages WHERE id = $%d::uuid) ", argIdx))
		args = append(args, in.Cursor)
		argIdx++
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY m.created_at DESC LIMIT $%d ", argIdx))
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return MessagePage{}, fmt.Errorf("search messages failed: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var createdAt time.Time
		var editedAt *time.Time

		err := rows.Scan(
			&msg.ID, &msg.ChannelID, &msg.SenderID, &msg.SenderName,
			&msg.SenderAvatar, &msg.SenderRole, &msg.Content,
			&createdAt, &editedAt, &msg.IsDeleted, &msg.ClientMsgID,
			&msg.ReplyToID, &msg.ThreadCount, &msg.Pinned, &msg.Saved,
		)
		if err != nil {
			return MessagePage{}, err
		}
		msg.Timestamp = createdAt.Format(time.RFC3339)
		if editedAt != nil {
			msg.EditedAt = editedAt.Format(time.RFC3339)
		}
		msg.Reactions = make(map[string][]string)
		messages = append(messages, msg)
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	var nextCursor string
	if len(messages) > 0 {
		nextCursor = messages[len(messages)-1].ID
	}

	if len(messages) > 0 {
		_ = s.batchLoadMessageRelations(ctx, actor.OrganizationID, messages)
	}

	return MessagePage{
		Messages:   messages,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) SendMessage(ctx context.Context, actor Actor, in SendMessageInput) (Message, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, in.ChannelID, actor.EmployeeID, actor.IsAdmin) {
		return Message{}, ErrForbidden
	}

	var realChannelID string
	var isArchived bool
	var chName string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, is_archived, name FROM chat_channels
		WHERE organization_id = $1::uuid 
		  AND (
		      (CASE WHEN $2 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id = $2::uuid ELSE false END)
		      OR lower(name) = lower($2)
		      OR lower(name) = replace(lower($2), 'ch-', '')
		  )
		LIMIT 1
	`, actor.OrganizationID, in.ChannelID).Scan(&realChannelID, &isArchived, &chName)
	if err != nil {
		return Message{}, ErrNotFound
	}
	in.ChannelID = realChannelID

	if isArchived {
		return Message{}, ErrChannelArchived
	}

	// Announcements restriction: only HR and Admins can post in #announcements
	if strings.EqualFold(chName, "announcements") && !actor.IsAdmin {
		return Message{}, fmt.Errorf("%w: only HR and Administrators can post in #announcements", ErrForbidden)
	}

	// Idempotency: return existing message if clientMsgId was already sent
	if in.ClientMsgID != "" {
		var existingID string
		_ = s.pool.QueryRow(ctx, `
			SELECT id::text FROM chat_messages
			WHERE organization_id = $1::uuid AND channel_id = $2::uuid 
			  AND (
			      sender_employee_id = (CASE WHEN $3 <> '' AND $3 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $3::uuid ELSE NULL END)
			      OR sender_user_id = (CASE WHEN $3 <> '' AND $3 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $3::uuid ELSE NULL END)
			  )
			  AND client_msg_id = $4
		`, actor.OrganizationID, in.ChannelID, actor.EmployeeID, in.ClientMsgID).Scan(&existingID)
		if existingID != "" {
			page, err := s.ListMessages(ctx, actor, ListMessagesFilter{
				ChannelID:      in.ChannelID,
				IncludeThreads: true,
				Limit:          1,
			})
			if err == nil {
				for _, m := range page.Messages {
					if m.ID == existingID {
						return m, nil
					}
				}
			}
		}
	}

	mentions := s.extractMentions(in.Content)
	msgID := newUUID()
	now := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Message{}, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO chat_messages (
			id, organization_id, channel_id, parent_message_id,
			sender_user_id, sender_employee_id, sender_name,
			sender_avatar, sender_role, content, client_msg_id,
			created_at
		) VALUES (
			$1::uuid, $2::uuid, $3::uuid, 
			CASE WHEN $4 <> '' AND $4 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $4::uuid ELSE NULL END,
			CASE WHEN $5 <> '' AND $5 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $5::uuid ELSE NULL END,
			CASE WHEN $6 <> '' AND $6 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $6::uuid ELSE NULL END,
			$7, $8, $9, $10, NULLIF($11, ''),
			$12
		)
	`, msgID, actor.OrganizationID, in.ChannelID, in.ReplyToID,
		actor.UserID, actor.EmployeeID, actor.FullName,
		actor.AvatarURL, actor.RoleTitle, in.Content, in.ClientMsgID,
		now)
	if err != nil {
		return Message{}, fmt.Errorf("insert message failed: %w", err)
	}

	// If threaded reply, increment parent message reply_count
	if in.ReplyToID != "" {
		_, _ = tx.Exec(ctx, `
			UPDATE chat_messages
			SET reply_count = reply_count + 1, last_reply_at = $1
			WHERE organization_id = $2::uuid AND id = $3::uuid
		`, now, actor.OrganizationID, in.ReplyToID)
	}

	// Update channel last_message_at
	_, _ = tx.Exec(ctx, `
		UPDATE chat_channels
		SET last_message_at = $1, updated_at = $1
		WHERE organization_id = $2::uuid AND id = $3::uuid
	`, now, actor.OrganizationID, in.ChannelID)

	// Attachments association
	var attachments []Attachment
	var validAttachmentIDs []string
	for _, id := range in.AttachmentIDs {
		if uuidRegex.MatchString(id) {
			validAttachmentIDs = append(validAttachmentIDs, id)
		}
	}
	for _, a := range in.Attachments {
		if a.ID != "" && uuidRegex.MatchString(a.ID) {
			validAttachmentIDs = append(validAttachmentIDs, a.ID)
		} else if a.URL != "" {
			customAttID := newUUID()
			mediaType := a.Type
			if mediaType == "" {
				mediaType = classifyMimeType(a.MimeType, a.Name)
			}
			_, _ = tx.Exec(ctx, `
				INSERT INTO chat_attachments (
					id, organization_id, channel_id, message_id,
					uploaded_by_employee_id, uploaded_by_user_id,
					filename, media_type, content_type, size_bytes,
					external_url, created_at
				) VALUES (
					$1::uuid, $2::uuid, $3::uuid, $4::uuid,
					CASE WHEN $5 <> '' AND $5 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $5::uuid ELSE NULL END,
					CASE WHEN $6 <> '' AND $6 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $6::uuid ELSE NULL END,
					$7, $8, $9, $10,
					$11, NOW()
				)
			`, customAttID, actor.OrganizationID, in.ChannelID, msgID,
				actor.EmployeeID, actor.UserID,
				a.Name, mediaType, a.MimeType, a.Size, a.URL)
			validAttachmentIDs = append(validAttachmentIDs, customAttID)
		}
	}
	if len(validAttachmentIDs) > 0 {
		_, err = tx.Exec(ctx, `
			UPDATE chat_attachments
			SET message_id = $1::uuid
			WHERE organization_id = $2::uuid AND id = ANY($3::uuid[])
		`, msgID, actor.OrganizationID, validAttachmentIDs)
		if err != nil {
			return Message{}, err
		}

		rows, err := tx.Query(ctx, `
			SELECT id::text, filename, content_type, media_type, size_bytes, COALESCE(external_url, '')
			FROM chat_attachments
			WHERE message_id = $1::uuid
		`, msgID)
		if err == nil {
			for rows.Next() {
				var att Attachment
				var extURL string
				if err := rows.Scan(&att.ID, &att.Name, &att.MimeType, &att.Type, &att.Size, &extURL); err == nil {
					if extURL != "" {
						att.URL = extURL
					} else {
						att.URL = s.SignAttachmentURL(att.ID)
					}
					attachments = append(attachments, att)
				}
			}
			rows.Close()
		}
	}

	// Poll creation
	var createdPoll *Poll
	if in.Poll != nil && in.Poll.Question != "" && len(in.Poll.Options) >= 2 {
		pollID := newUUID()
		_, err = tx.Exec(ctx, `
			INSERT INTO chat_polls (id, message_id, organization_id, question, allow_multiple, is_anonymous, is_closed, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, false, $7)
		`, pollID, msgID, actor.OrganizationID, in.Poll.Question, in.Poll.AllowMultiple, in.Poll.IsAnonymous, now)
		if err != nil {
			return Message{}, err
		}

		createdPoll = &Poll{
			Question:      in.Poll.Question,
			AllowMultiple: in.Poll.AllowMultiple,
			IsAnonymous:   in.Poll.IsAnonymous,
			Closed:        false,
		}

		for idx, opt := range in.Poll.Options {
			optID := newUUID()
			_, err = tx.Exec(ctx, `
				INSERT INTO chat_poll_options (id, poll_id, option_text, sort_order)
				VALUES ($1::uuid, $2::uuid, $3, $4)
			`, optID, pollID, opt.Text, idx)
			if err != nil {
				return Message{}, err
			}
			createdPoll.Options = append(createdPoll.Options, PollOption{
				ID:       optID,
				Text:     opt.Text,
				VoterIDs: []string{},
			})
		}
	}

	// Automatically update sender's last_read timestamp for this channel
	_, _ = tx.Exec(ctx, `
		INSERT INTO chat_channel_members (channel_id, organization_id, employee_id, user_id, last_read_message_id, last_read_at, unread_count)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NULLIF($4, '')::uuid, $5::uuid, $6, 0)
		ON CONFLICT (channel_id, employee_id)
		DO UPDATE SET last_read_message_id = EXCLUDED.last_read_message_id, last_read_at = EXCLUDED.last_read_at, unread_count = 0
	`, in.ChannelID, actor.OrganizationID, actor.EmployeeID, actor.UserID, msgID, now)

	eventType := EventMessageCreated
	if in.ReplyToID != "" {
		eventType = EventThreadReplyCreated
	}

	msg := Message{
		ID:           msgID,
		ChannelID:    in.ChannelID,
		SenderID:     actor.EmployeeID,
		SenderName:   actor.FullName,
		SenderAvatar: actor.AvatarURL,
		SenderRole:   actor.RoleTitle,
		Content:      in.Content,
		Timestamp:    now.Format(time.RFC3339),
		ClientMsgID:  in.ClientMsgID,
		Attachments:  attachments,
		Poll:         createdPoll,
		Reactions:    make(map[string][]string),
		ReplyToID:    in.ReplyToID,
		Mentions:     mentions,
	}

	payloadRaw, _ := json.Marshal(msg)
	_, _ = tx.Exec(ctx, `
		INSERT INTO chat_events (organization_id, channel_id, actor_employee_id, event_type, payload, created_at)
		VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, $5, $6)
	`, actor.OrganizationID, in.ChannelID, actor.EmployeeID, eventType, payloadRaw, now)

	if err := tx.Commit(ctx); err != nil {
		return Message{}, err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           eventType,
		OrganizationID: actor.OrganizationID,
		ChannelID:      in.ChannelID,
		ActorID:        actor.EmployeeID,
		Payload:        msg,
		Timestamp:      now.Format(time.RFC3339),
	})

	go s.dispatchNotifications(actor, in.ChannelID, msgID, in.Content, in.ReplyToID, mentions)

	return msg, nil
}

func (s *Service) EditMessage(ctx context.Context, actor Actor, channelID, messageID, newContent string) (Message, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return Message{}, ErrForbidden
	}

	var senderID *string
	var isDeleted bool
	err := s.pool.QueryRow(ctx, `
		SELECT sender_employee_id::text, (deleted_at IS NOT NULL)
		FROM chat_messages
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, messageID).Scan(&senderID, &isDeleted)
	if err != nil {
		return Message{}, ErrNotFound
	}
	if isDeleted {
		return Message{}, ErrNotFound
	}
	if (senderID == nil || *senderID != actor.EmployeeID) && !actor.IsAdmin {
		return Message{}, ErrForbidden
	}

	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `
		UPDATE chat_messages
		SET content = $1, edited_at = $2
		WHERE organization_id = $3::uuid AND id = $4::uuid
	`, newContent, now, actor.OrganizationID, messageID)
	if err != nil {
		return Message{}, err
	}

	page, err := s.ListMessages(ctx, actor, ListMessagesFilter{
		ChannelID:      channelID,
		IncludeThreads: true,
		Limit:          1,
	})
	var updated Message
	if err == nil {
		for _, m := range page.Messages {
			if m.ID == messageID {
				updated = m
				break
			}
		}
	}
	if updated.ID == "" {
		updated = Message{
			ID:        messageID,
			ChannelID: channelID,
			Content:   newContent,
			EditedAt:  now.Format(time.RFC3339),
		}
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessageUpdated,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload:        updated,
		Timestamp:      now.Format(time.RFC3339),
	})

	return updated, nil
}

func (s *Service) DeleteMessage(ctx context.Context, actor Actor, channelID, messageID string) error {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return ErrForbidden
	}

	var senderID *string
	err := s.pool.QueryRow(ctx, `
		SELECT sender_employee_id::text
		FROM chat_messages
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, messageID).Scan(&senderID)
	if err != nil {
		return ErrNotFound
	}
	if (senderID == nil || *senderID != actor.EmployeeID) && !actor.IsAdmin {
		return ErrForbidden
	}

	_, err = s.pool.Exec(ctx, `
		UPDATE chat_messages
		SET deleted_at = NOW(), content = 'This message was deleted.', edited_at = NOW()
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, messageID)
	if err != nil {
		return err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessageDeleted,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"messageId": messageID,
			"channelId": channelID,
			"isDeleted": true,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return nil
}

func (s *Service) TogglePinMessage(ctx context.Context, actor Actor, channelID, messageID string) (bool, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return false, ErrForbidden
	}

	var currentlyPinned bool
	err := s.pool.QueryRow(ctx, `
		SELECT is_pinned FROM chat_messages
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, messageID).Scan(&currentlyPinned)
	if err != nil {
		return false, ErrNotFound
	}

	newPinned := !currentlyPinned
	now := time.Now().UTC()

	_, err = s.pool.Exec(ctx, `
		UPDATE chat_messages
		SET is_pinned = $1, pinned_by_user_id = NULLIF($2, '')::uuid, pinned_at = $3
		WHERE organization_id = $4::uuid AND id = $5::uuid
	`, newPinned, actor.UserID, now, actor.OrganizationID, messageID)
	if err != nil {
		return false, err
	}

	eventType := EventMessagePinned
	if !newPinned {
		eventType = EventMessageUnpinned
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           eventType,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"messageId": messageID,
			"channelId": channelID,
			"pinned":    newPinned,
		},
		Timestamp: now.Format(time.RFC3339),
	})

	return newPinned, nil
}

func (s *Service) ToggleSaveMessage(ctx context.Context, actor Actor, messageID string) (bool, error) {
	var existing string
	_ = s.pool.QueryRow(ctx, `
		SELECT message_id::text FROM chat_message_saves
		WHERE organization_id = $1::uuid AND employee_id = $2::uuid AND message_id = $3::uuid
	`, actor.OrganizationID, actor.EmployeeID, messageID).Scan(&existing)

	if existing != "" {
		_, err := s.pool.Exec(ctx, `
			DELETE FROM chat_message_saves
			WHERE organization_id = $1::uuid AND employee_id = $2::uuid AND message_id = $3::uuid
		`, actor.OrganizationID, actor.EmployeeID, messageID)
		return false, err
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_message_saves (organization_id, employee_id, message_id, created_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, NOW())
		ON CONFLICT DO NOTHING
	`, actor.OrganizationID, actor.EmployeeID, messageID)
	return true, err
}

func (s *Service) ToggleReaction(ctx context.Context, actor Actor, channelID, messageID, emoji string) (map[string][]string, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return nil, ErrForbidden
	}

	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return nil, ErrInvalidInput
	}

	var existing string
	_ = s.pool.QueryRow(ctx, `
		SELECT message_id::text FROM chat_message_reactions
		WHERE organization_id = $1::uuid AND message_id = $2::uuid AND employee_id = $3::uuid AND emoji = $4
	`, actor.OrganizationID, messageID, actor.EmployeeID, emoji).Scan(&existing)

	eventType := EventMessageReactionAdded
	if existing != "" {
		_, _ = s.pool.Exec(ctx, `
			DELETE FROM chat_message_reactions
			WHERE organization_id = $1::uuid AND message_id = $2::uuid AND employee_id = $3::uuid AND emoji = $4
		`, actor.OrganizationID, messageID, actor.EmployeeID, emoji)
		eventType = EventMessageReactionRemove
	} else {
		_, _ = s.pool.Exec(ctx, `
			INSERT INTO chat_message_reactions (organization_id, message_id, emoji, employee_id, user_id, created_at)
			VALUES ($1::uuid, $2::uuid, $3, $4::uuid, NULLIF($5, '')::uuid, NOW())
			ON CONFLICT DO NOTHING
		`, actor.OrganizationID, messageID, emoji, actor.EmployeeID, actor.UserID)
	}

	reactions := make(map[string][]string)
	rows, err := s.pool.Query(ctx, `
		SELECT emoji, employee_id::text
		FROM chat_message_reactions
		WHERE organization_id = $1::uuid AND message_id = $2::uuid
	`, actor.OrganizationID, messageID)
	if err == nil {
		for rows.Next() {
			var em, eid string
			if err := rows.Scan(&em, &eid); err == nil {
				reactions[em] = append(reactions[em], eid)
			}
		}
		rows.Close()
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           eventType,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"messageId": messageID,
			"channelId": channelID,
			"emoji":     emoji,
			"reactions": reactions,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return reactions, nil
}

func (s *Service) VotePoll(ctx context.Context, actor Actor, channelID, messageID, optionID string) (*Poll, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return nil, ErrForbidden
	}

	var pollID string
	var allowMultiple, isClosed bool
	err := s.pool.QueryRow(ctx, `
		SELECT p.id::text, p.allow_multiple, p.is_closed
		FROM chat_polls p
		JOIN chat_messages m ON m.id = p.message_id
		WHERE m.organization_id = $1::uuid AND m.id = $2::uuid
	`, actor.OrganizationID, messageID).Scan(&pollID, &allowMultiple, &isClosed)
	if err != nil {
		return nil, ErrNotFound
	}
	if isClosed {
		return nil, fmt.Errorf("%w: poll is closed", ErrInvalidInput)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var count int
	_ = tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM chat_poll_votes
		WHERE poll_id = $1::uuid AND option_id = $2::uuid AND employee_id = $3::uuid
	`, pollID, optionID, actor.EmployeeID).Scan(&count)
	hasVotedThisOption := count > 0

	if hasVotedThisOption {
		_, _ = tx.Exec(ctx, `
			DELETE FROM chat_poll_votes
			WHERE poll_id = $1::uuid AND option_id = $2::uuid AND employee_id = $3::uuid
		`, pollID, optionID, actor.EmployeeID)
	} else {
		if !allowMultiple {
			_, _ = tx.Exec(ctx, `
				DELETE FROM chat_poll_votes
				WHERE poll_id = $1::uuid AND employee_id = $2::uuid
			`, pollID, actor.EmployeeID)
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO chat_poll_votes (poll_id, option_id, employee_id, created_at)
			VALUES ($1::uuid, $2::uuid, $3::uuid, NOW())
			ON CONFLICT DO NOTHING
		`, pollID, optionID, actor.EmployeeID)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	poll, err := s.loadPoll(ctx, messageID)
	if err != nil {
		return nil, err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessagePollUpdated,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"messageId": messageID,
			"channelId": channelID,
			"poll":      poll,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return poll, nil
}

func (s *Service) ToggleClosePoll(ctx context.Context, actor Actor, channelID, messageID string) (*Poll, error) {
	if !s.AuthorizeChannel(ctx, actor.OrganizationID, channelID, actor.EmployeeID, actor.IsAdmin) {
		return nil, ErrForbidden
	}

	_, err := s.pool.Exec(ctx, `
		UPDATE chat_polls
		SET is_closed = NOT is_closed
		WHERE message_id = $1::uuid
	`, messageID)
	if err != nil {
		return nil, err
	}

	poll, err := s.loadPoll(ctx, messageID)
	if err != nil {
		return nil, err
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventMessagePollUpdated,
		OrganizationID: actor.OrganizationID,
		ChannelID:      channelID,
		ActorID:        actor.EmployeeID,
		Payload: map[string]any{
			"messageId": messageID,
			"channelId": channelID,
			"poll":      poll,
		},
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})

	return poll, nil
}

func (s *Service) UploadAttachment(ctx context.Context, actor Actor, in UploadAttachmentInput) (Attachment, error) {
	if in.SizeBytes > MaxAttachmentSize {
		return Attachment{}, ErrPayloadTooLarge
	}
	if in.ChannelID != "" && !s.AuthorizeChannel(ctx, actor.OrganizationID, in.ChannelID, actor.EmployeeID, actor.IsAdmin) {
		return Attachment{}, ErrForbidden
	}

	var channelUUID *string
	if in.ChannelID != "" {
		var resolvedID string
		err := s.pool.QueryRow(ctx, `
			SELECT id::text FROM chat_channels 
			WHERE organization_id = $1::uuid 
			  AND (
			      (CASE WHEN $2 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN id = $2::uuid ELSE false END)
			      OR lower(name) = lower($2)
			      OR lower(name) = replace(lower($2), 'ch-', '')
			  )
			LIMIT 1
		`, actor.OrganizationID, in.ChannelID).Scan(&resolvedID)
		if err == nil && resolvedID != "" {
			channelUUID = &resolvedID
		}
	}

	channelParam := ""
	if channelUUID != nil {
		channelParam = *channelUUID
	}

	mimeType := in.ContentType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = http.DetectContentType(in.Data)
	}
	fileType := classifyMimeType(mimeType, in.FileName)

	attID := newUUID()
	objectKey := fmt.Sprintf("chat/%s/%s/%s", actor.OrganizationID, attID, in.FileName)

	var inlineData []byte
	if s.storage != nil {
		err := s.storage.PutObject(ctx, objectKey, mimeType, bytes.NewReader(in.Data), in.SizeBytes)
		if err != nil {
			s.logger.Warn("storage upload failed, falling back to database inline data", "err", err)
			inlineData = in.Data
			objectKey = ""
		}
	} else {
		inlineData = in.Data
		objectKey = ""
	}

	sizeLabel := fmt.Sprintf("%.1f MB", float64(in.SizeBytes)/(1024*1024))

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_attachments (
			id, organization_id, channel_id, uploaded_by_employee_id, uploaded_by_user_id,
			filename, media_type, content_type, size_bytes, size_label,
			object_key, inline_data, created_at
		) VALUES (
			$1::uuid, $2::uuid, 
			CASE WHEN $3 <> '' AND $3 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $3::uuid ELSE NULL END,
			CASE WHEN $4 <> '' AND $4 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $4::uuid ELSE NULL END,
			CASE WHEN $5 <> '' AND $5 ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN $5::uuid ELSE NULL END,
			$6, $7, $8, $9, $10,
			$11, $12, NOW()
		)
	`, attID, actor.OrganizationID, channelParam, actor.EmployeeID, actor.UserID,
		in.FileName, fileType, mimeType, in.SizeBytes, sizeLabel,
		objectKey, inlineData)
	if err != nil {
		return Attachment{}, fmt.Errorf("insert attachment failed: %w", err)
	}

	return Attachment{
		ID:       attID,
		Name:     in.FileName,
		URL:      s.SignAttachmentURL(attID),
		Type:     fileType,
		MimeType: mimeType,
		Size:     in.SizeBytes,
	}, nil
}

func (s *Service) GetAttachmentContent(ctx context.Context, attachmentID, token string, actor *Actor) (*AttachmentRecord, io.ReadCloser, error) {
	var rec AttachmentRecord
	var channelID *string
	err := s.pool.QueryRow(ctx, `
		SELECT 
			id::text, organization_id::text, COALESCE(channel_id::text, ''), 
			COALESCE(message_id::text, ''), COALESCE(uploaded_by_employee_id::text, ''), 
			filename, media_type, content_type, size_bytes, object_key, inline_data, created_at
		FROM chat_attachments
		WHERE id = $1::uuid
	`, attachmentID).Scan(
		&rec.ID, &rec.OrganizationID, &channelID,
		&rec.MessageID, &rec.UploadedBy, &rec.FileName,
		&rec.FileType, &rec.MimeType, &rec.SizeBytes,
		&rec.StorageKey, &rec.InlineData, &rec.CreatedAt,
	)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	if channelID != nil {
		rec.ChannelID = *channelID
	}

	validToken := s.VerifyAttachmentToken(rec.ID, token)
	if !validToken {
		if actor == nil || actor.OrganizationID != rec.OrganizationID {
			return nil, nil, ErrForbidden
		}
		if rec.ChannelID != "" && !s.AuthorizeChannel(ctx, actor.OrganizationID, rec.ChannelID, actor.EmployeeID, actor.IsAdmin) {
			return nil, nil, ErrForbidden
		}
	}

	if len(rec.InlineData) > 0 {
		return &rec, io.NopCloser(bytes.NewReader(rec.InlineData)), nil
	}

	if s.storage != nil && rec.StorageKey != "" {
		rc, _, _, err := s.storage.GetObject(ctx, rec.StorageKey)
		if err != nil {
			return nil, nil, err
		}
		return &rec, rc, nil
	}

	return nil, nil, ErrNotFound
}

func (s *Service) SignAttachmentURL(attachmentID string) string {
	exp := time.Now().Add(48 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", attachmentID, exp)
	mac := hmac.New(sha256.New, s.signingSecret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))
	token := fmt.Sprintf("%d.%s", exp, sig)
	return fmt.Sprintf("/api/v1/chat/attachments/%s/content?token=%s", attachmentID, token)
}

func (s *Service) VerifyAttachmentToken(attachmentID, token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	expUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expUnix {
		return false
	}
	payload := fmt.Sprintf("%s:%d", attachmentID, expUnix)
	mac := hmac.New(sha256.New, s.signingSecret)
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expectedSig))
}

func (s *Service) UpdatePresence(ctx context.Context, actor Actor, status, statusText string, dnd bool, dndUntil *time.Time) (Presence, error) {
	if status == "" {
		status = "online"
	}
	now := time.Now().UTC()

	_, err := s.pool.Exec(ctx, `
		INSERT INTO chat_user_presence (
			organization_id, employee_id, user_id, status,
			custom_status, dnd_until, last_seen_at, updated_at
		) VALUES (
			$1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4,
			$5, $6, $7, $7
		)
		ON CONFLICT (organization_id, employee_id)
		DO UPDATE SET 
			status = EXCLUDED.status,
			custom_status = EXCLUDED.custom_status,
			dnd_until = EXCLUDED.dnd_until,
			last_seen_at = EXCLUDED.last_seen_at,
			updated_at = EXCLUDED.updated_at
	`, actor.OrganizationID, actor.EmployeeID, actor.UserID, status,
		statusText, dndUntil, now)
	if err != nil {
		return Presence{}, err
	}

	p := Presence{
		EmployeeID:   actor.EmployeeID,
		UserID:       actor.UserID,
		Status:       status,
		StatusText:   statusText,
		DoNotDisturb: dnd,
		LastSeenAt:   now.Format(time.RFC3339),
	}
	if dndUntil != nil {
		p.DndUntil = dndUntil.Format(time.RFC3339)
	}

	s.hub.Publish(RealtimeEvent{
		Version:        EventVersionV1,
		Type:           EventPresenceUpdated,
		OrganizationID: actor.OrganizationID,
		ActorID:        actor.EmployeeID,
		Payload:        p,
		Timestamp:      now.Format(time.RFC3339),
	})

	return p, nil
}

func (s *Service) ListPresence(ctx context.Context, orgID string) ([]Presence, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT employee_id::text, COALESCE(user_id::text, ''), status, COALESCE(custom_status, ''), (dnd_until IS NOT NULL), dnd_until, last_seen_at
		FROM chat_user_presence
		WHERE organization_id = $1::uuid
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	onlineMap := s.hub.OnlineEmployeeIDs(orgID)

	var list []Presence
	for rows.Next() {
		var p Presence
		var dndUntil *time.Time
		var lastSeenAt time.Time

		err := rows.Scan(
			&p.EmployeeID, &p.UserID, &p.Status, &p.StatusText,
			&p.DoNotDisturb, &dndUntil, &lastSeenAt,
		)
		if err != nil {
			return nil, err
		}
		if dndUntil != nil {
			p.DndUntil = dndUntil.Format(time.RFC3339)
		}
		p.LastSeenAt = lastSeenAt.Format(time.RFC3339)

		if onlineMap[p.EmployeeID] && p.Status == "offline" {
			p.Status = "online"
		}

		list = append(list, p)
	}

	return list, nil
}

func (s *Service) batchLoadMessageRelations(ctx context.Context, orgID string, msgs []Message) error {
	msgIDs := make([]string, len(msgs))
	msgMap := make(map[string]*Message, len(msgs))
	for i := range msgs {
		msgIDs[i] = msgs[i].ID
		msgMap[msgs[i].ID] = &msgs[i]
	}

	// 1. Attachments
	attRows, err := s.pool.Query(ctx, `
		SELECT id::text, message_id::text, filename, media_type, content_type, size_bytes, COALESCE(external_url, '')
		FROM chat_attachments
		WHERE organization_id = $1::uuid AND message_id = ANY($2::uuid[])
	`, orgID, msgIDs)
	if err == nil {
		for attRows.Next() {
			var att Attachment
			var mid, extURL string
			if err := attRows.Scan(&att.ID, &mid, &att.Name, &att.Type, &att.MimeType, &att.Size, &extURL); err == nil {
				if extURL != "" {
					att.URL = extURL
				} else {
					att.URL = s.SignAttachmentURL(att.ID)
				}
				if m, ok := msgMap[mid]; ok {
					m.Attachments = append(m.Attachments, att)
				}
			}
		}
		attRows.Close()
	}

	// 2. Reactions
	reactRows, err := s.pool.Query(ctx, `
		SELECT message_id::text, emoji, employee_id::text
		FROM chat_message_reactions
		WHERE organization_id = $1::uuid AND message_id = ANY($2::uuid[])
	`, orgID, msgIDs)
	if err == nil {
		for reactRows.Next() {
			var mid, emoji, eid string
			if err := reactRows.Scan(&mid, &emoji, &eid); err == nil {
				if m, ok := msgMap[mid]; ok {
					if m.Reactions == nil {
						m.Reactions = make(map[string][]string)
					}
					m.Reactions[emoji] = append(m.Reactions[emoji], eid)
				}
			}
		}
		reactRows.Close()
	}

	// 3. Polls
	pollRows, err := s.pool.Query(ctx, `
		SELECT p.id::text, p.message_id::text, p.question, p.allow_multiple, p.is_anonymous, p.is_closed
		FROM chat_polls p
		WHERE p.organization_id = $1::uuid AND p.message_id = ANY($2::uuid[])
	`, orgID, msgIDs)
	if err == nil {
		type pollRef struct {
			pollID string
			msgID  string
			poll   *Poll
		}
		var polls []pollRef
		pollIDList := make([]string, 0)

		for pollRows.Next() {
			var pr pollRef
			pr.poll = &Poll{}
			if err := pollRows.Scan(&pr.pollID, &pr.msgID, &pr.poll.Question, &pr.poll.AllowMultiple, &pr.poll.IsAnonymous, &pr.poll.Closed); err == nil {
				polls = append(polls, pr)
				pollIDList = append(pollIDList, pr.pollID)
			}
		}
		pollRows.Close()

		if len(pollIDList) > 0 {
			optRows, err := s.pool.Query(ctx, `
				SELECT o.id::text, o.poll_id::text, o.option_text, COALESCE(array_agg(v.employee_id::text) FILTER (WHERE v.employee_id IS NOT NULL), ARRAY[]::text[])
				FROM chat_poll_options o
				LEFT JOIN chat_poll_votes v ON v.option_id = o.id
				WHERE o.poll_id = ANY($1::uuid[])
				GROUP BY o.id, o.poll_id, o.sort_order, o.option_text
				ORDER BY o.sort_order ASC
			`, pollIDList)
			if err == nil {
				optMap := make(map[string][]PollOption)
				for optRows.Next() {
					var opt PollOption
					var pid string
					if err := optRows.Scan(&opt.ID, &pid, &opt.Text, &opt.VoterIDs); err == nil {
						optMap[pid] = append(optMap[pid], opt)
					}
				}
				optRows.Close()

				for _, pr := range polls {
					pr.poll.Options = optMap[pr.pollID]
					if pr.poll.Options == nil {
						pr.poll.Options = []PollOption{}
					}
					if m, ok := msgMap[pr.msgID]; ok {
						m.Poll = pr.poll
					}
				}
			}
		}
	}

	return nil
}

func (s *Service) loadPoll(ctx context.Context, messageID string) (*Poll, error) {
	var pollID string
	poll := &Poll{}
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, question, allow_multiple, is_anonymous, is_closed
		FROM chat_polls
		WHERE message_id = $1::uuid
	`, messageID).Scan(&pollID, &poll.Question, &poll.AllowMultiple, &poll.IsAnonymous, &poll.Closed)
	if err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT o.id::text, o.option_text, COALESCE(array_agg(v.employee_id::text) FILTER (WHERE v.employee_id IS NOT NULL), ARRAY[]::text[])
		FROM chat_poll_options o
		LEFT JOIN chat_poll_votes v ON v.option_id = o.id
		WHERE o.poll_id = $1::uuid
		GROUP BY o.id, o.sort_order, o.option_text
		ORDER BY o.sort_order ASC
	`, pollID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var opt PollOption
		if err := rows.Scan(&opt.ID, &opt.Text, &opt.VoterIDs); err == nil {
			poll.Options = append(poll.Options, opt)
		}
	}
	return poll, nil
}

func (s *Service) extractMentions(content string) []string {
	matches := mentionRegex.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	set := make(map[string]bool)
	var list []string
	for _, m := range matches {
		if len(m) > 1 {
			target := strings.ToLower(m[1])
			if !set[target] {
				set[target] = true
				list = append(list, target)
			}
		}
	}
	return list
}

func (s *Service) dispatchNotifications(actor Actor, channelID, msgID, content, replyToID string, mentions []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var chName, chType string
	_ = s.pool.QueryRow(ctx, `
		SELECT name, channel_type FROM chat_channels
		WHERE organization_id = $1::uuid AND id = $2::uuid
	`, actor.OrganizationID, channelID).Scan(&chName, &chType)

	preview := content
	if len(preview) > 120 {
		preview = preview[:117] + "..."
	}

	type recipient struct {
		userID     string
		employeeID string
		isMuted    bool
		dnd        bool
	}

	var recipients []recipient
	query := `
		SELECT 
			COALESCE(e.user_id::text, ''),
			m.employee_id::text,
			m.is_muted,
			(p.dnd_until IS NOT NULL AND p.dnd_until > NOW())
		FROM chat_channel_members m
		JOIN employees e ON e.id = m.employee_id
		LEFT JOIN chat_user_presence p ON p.employee_id = m.employee_id
		WHERE m.organization_id = $1::uuid 
		  AND m.channel_id = $2::uuid
		  AND m.employee_id != $3::uuid
	`
	rows, err := s.pool.Query(ctx, query, actor.OrganizationID, channelID, actor.EmployeeID)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var r recipient
		if err := rows.Scan(&r.userID, &r.employeeID, &r.isMuted, &r.dnd); err == nil && r.userID != "" {
			recipients = append(recipients, r)
		}
	}

	isDM := chType == ChannelTypeDM || chType == ChannelTypeGroupDM

	for _, rec := range recipients {
		if rec.isMuted || rec.dnd {
			continue
		}

		title := fmt.Sprintf("#%s: New message from %s", chName, actor.FullName)
		if isDM {
			title = fmt.Sprintf("Direct message from %s", actor.FullName)
		}

		shouldNotify := isDM
		for _, m := range mentions {
			if m == "channel" || m == "here" || strings.Contains(strings.ToLower(actor.FullName), m) {
				shouldNotify = true
				title = fmt.Sprintf("%s mentioned you in #%s", actor.FullName, chName)
				break
			}
		}

		if shouldNotify {
			_, _ = s.pool.Exec(ctx, `
				INSERT INTO user_notifications (organization_id, user_id, title, message, category, link, created_at)
				VALUES ($1::uuid, $2::uuid, $3, $4, 'chat', '/chat', NOW())
			`, actor.OrganizationID, rec.userID, title, preview)
		}
	}
}

func classifyMimeType(mime, filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if strings.HasPrefix(mime, "image/") || ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" || ext == ".svg" {
		return "image"
	}
	if strings.HasPrefix(mime, "video/") || ext == ".mp4" || ext == ".mov" || ext == ".webm" {
		return "video"
	}
	return "file"
}
