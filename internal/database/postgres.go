package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolConfig.MaxConns = 80
	poolConfig.MinConns = 15
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	if err := EnsureSchema(ctx, pool); err != nil {
		// Log error but don't prevent server start
		fmt.Printf("warning: schema ensure: %v\n", err)
	}
	return pool, nil
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	schemaSQL := `
		INSERT INTO permissions (key, description) VALUES
			('attendance:manage_activities', 'Create and manage attendance punch activities'),
			('announcements:manage', 'Create and delete company announcements'),
			('celebrations:wish', 'Send celebration wishes to teammates'),
			('leave:request_self', 'Submit leave requests for own employee record'),
			('attendance:punch_self', 'Clock in, clock out and record own breaks'),
			('attendance:read', 'View own attendance logs and status'),
			('signatures:read', 'View and sign documents assigned to self')
		ON CONFLICT (key) DO NOTHING;

		-- Grant essential self-service permissions to all employee roles across organizations
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, p.key
		FROM roles r
		CROSS JOIN (
			VALUES
				('leave:request_self'),
				('attendance:punch_self'),
				('attendance:read'),
				('celebrations:wish'),
				('signatures:read'),
				('organization:read'),
				('employees:read')
		) AS p(key)
		WHERE r.key = 'employee' OR r.display_name ILIKE '%employee%'
		ON CONFLICT DO NOTHING;

		-- Grant all permissions to owner and admin roles
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, p.key
		FROM roles r
		CROSS JOIN permissions p
		WHERE r.key = 'company_owner' OR r.key LIKE '%admin%' OR r.display_name ILIKE '%admin%' OR r.display_name ILIKE '%owner%'
		ON CONFLICT DO NOTHING;

		-- Grant leave and attendance management permissions to HR and Manager roles
		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, p.key
		FROM roles r
		CROSS JOIN (
			VALUES
				('leave:approve_hr'),
				('leave:approve_manager'),
				('leave:manage_policies'),
				('leave:request_any'),
				('attendance:manage_activities'),
				('attendance:read_team'),
				('attendance:manage'),
				('documents:upload'),
				('documents:read'),
				('documents:manage')
		) AS p(key)
		WHERE r.key ILIKE '%hr%' OR r.key ILIKE '%manager%' OR r.key ILIKE '%lead%' OR r.key ILIKE '%admin%' OR r.key ILIKE '%owner%'
		   OR r.display_name ILIKE '%hr%' OR r.display_name ILIKE '%manager%' OR r.display_name ILIKE '%lead%' OR r.display_name ILIKE '%admin%' OR r.display_name ILIKE '%owner%'
		ON CONFLICT DO NOTHING;

		CREATE TABLE IF NOT EXISTS attendance_activities (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name TEXT NOT NULL,
			code TEXT NOT NULL,
			is_billable BOOLEAN NOT NULL DEFAULT true,
			is_default BOOLEAN NOT NULL DEFAULT false,
			color TEXT NOT NULL DEFAULT '#7c3aed',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (organization_id, name)
		);
		CREATE INDEX IF NOT EXISTS attendance_activities_org_idx ON attendance_activities (organization_id);

		ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS activity TEXT;
		ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS is_billable BOOLEAN DEFAULT true;

		CREATE TABLE IF NOT EXISTS company_announcements (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'Company News',
			priority TEXT NOT NULL DEFAULT 'normal',
			author_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			author_name TEXT NOT NULL,
			author_role TEXT DEFAULT 'HR Management',
			pinned BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS company_announcements_org_idx ON company_announcements (organization_id, pinned DESC, created_at DESC);

		CREATE TABLE IF NOT EXISTS celebration_wishes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			recipient_employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			sender_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			sender_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			sender_name TEXT NOT NULL,
			sender_avatar TEXT,
			celebration_type TEXT NOT NULL DEFAULT 'birthday',
			message TEXT NOT NULL,
			reaction TEXT NOT NULL DEFAULT 'heart',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS celebration_wishes_org_recipient_idx ON celebration_wishes (organization_id, recipient_employee_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS user_notifications (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			message TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT 'general',
			link TEXT,
			is_read BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS user_notifications_user_idx ON user_notifications (organization_id, user_id, is_read, created_at DESC);

		CREATE TABLE IF NOT EXISTS user_preferences (
			user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			active_widgets JSONB NOT NULL DEFAULT '["feed","birthdays"]'::jsonb,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);

		-- Native e-signature engine: org-managed templates with merge fields
		INSERT INTO permissions (key, description) VALUES
			('signatures:manage', 'Create templates and send, void and audit signature requests')
		ON CONFLICT (key) DO NOTHING;

		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, 'signatures:manage'
		FROM roles r
		WHERE r.key = 'company_owner' OR r.key LIKE '%admin%' OR r.display_name ILIKE '%admin%' OR r.display_name ILIKE '%owner%' OR r.key ILIKE '%hr%' OR r.display_name ILIKE '%human resource%' OR r.display_name ILIKE '%hr%'
		ON CONFLICT DO NOTHING;

		CREATE TABLE IF NOT EXISTS signature_templates (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
			category TEXT NOT NULL DEFAULT 'General',
			description TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL,
			header_text TEXT NOT NULL DEFAULT '',
			footer_text TEXT NOT NULL DEFAULT '',
			requires_countersign BOOLEAN NOT NULL DEFAULT false,
			expires_in_days INTEGER NOT NULL DEFAULT 14 CHECK (expires_in_days BETWEEN 1 AND 365),
			is_active BOOLEAN NOT NULL DEFAULT true,
			created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (organization_id, name)
		);

		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS template_id UUID REFERENCES signature_templates(id) ON DELETE SET NULL;
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS title TEXT;
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS body_rendered TEXT;
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS body_sha256 CHAR(64);
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS header_rendered TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS footer_rendered TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS brand_color TEXT NOT NULL DEFAULT '#4F46E5';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS logo_url TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS website_url TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS contact_phone TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS contact_email TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_templates ADD COLUMN IF NOT EXISTS header_text TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_templates ADD COLUMN IF NOT EXISTS footer_text TEXT NOT NULL DEFAULT '';
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS voided_at TIMESTAMPTZ;
		ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS void_reason TEXT;
		ALTER TABLE signature_requests DROP CONSTRAINT IF EXISTS signature_requests_status_check;
		ALTER TABLE signature_requests ADD CONSTRAINT signature_requests_status_check CHECK (status IN ('draft','pending','sent','delivered','completed','declined','voided'));
		ALTER TABLE signature_requests DROP CONSTRAINT IF EXISTS signature_requests_provider_check;
		ALTER TABLE signature_requests ADD CONSTRAINT signature_requests_provider_check CHECK (provider IN ('docusign', 'native'));

		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS signature_type TEXT;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS signature_data TEXT;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS signer_ip TEXT;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS signer_user_agent TEXT;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS consent_at TIMESTAMPTZ;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS decline_reason TEXT;
		ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS declined_at TIMESTAMPTZ;
		ALTER TABLE signature_recipients DROP CONSTRAINT IF EXISTS signature_recipients_status_check;
		ALTER TABLE signature_recipients ADD CONSTRAINT signature_recipients_status_check CHECK (status IN ('created','pending','sent','delivered','completed','declined'));
		CREATE INDEX IF NOT EXISTS signature_recipients_employee_idx ON signature_recipients (employee_id);
		CREATE INDEX IF NOT EXISTS signature_recipients_user_idx ON signature_recipients (user_id);

		CREATE TABLE IF NOT EXISTS signature_events (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			signature_request_id UUID NOT NULL REFERENCES signature_requests(id) ON DELETE CASCADE,
			recipient_id UUID REFERENCES signature_recipients(id) ON DELETE SET NULL,
			actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			event_type TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT '',
			ip_address TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS signature_events_request_idx ON signature_events (signature_request_id, created_at);

		-- Workspace Chat Core Schema
		INSERT INTO permissions (key, description) VALUES
			('chat:read', 'View accessible workspace channels and messages'),
			('chat:write', 'Send messages, replies, reactions, and upload chat attachments'),
			('chat:manage_channels', 'Create, archive, and manage workspace channels and memberships')
		ON CONFLICT (key) DO NOTHING;

		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, p.key
		FROM roles r
		CROSS JOIN (
			VALUES ('chat:read'), ('chat:write')
		) AS p(key)
		ON CONFLICT DO NOTHING;

		INSERT INTO role_permissions (role_id, permission_key)
		SELECT r.id, 'chat:manage_channels'
		FROM roles r
		WHERE r.key = 'company_owner'
		   OR r.key ILIKE '%admin%'
		   OR r.key ILIKE '%hr%'
		   OR r.key ILIKE '%manager%'
		   OR r.display_name ILIKE '%owner%'
		   OR r.display_name ILIKE '%admin%'
		   OR r.display_name ILIKE '%hr%'
		   OR r.display_name ILIKE '%manager%'
		ON CONFLICT DO NOTHING;

		CREATE TABLE IF NOT EXISTS chat_channels (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
			channel_type TEXT NOT NULL DEFAULT 'channel' CHECK (channel_type IN ('channel', 'dm', 'group_dm')),
			topic TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			is_private BOOLEAN NOT NULL DEFAULT false,
			department_restricted BOOLEAN NOT NULL DEFAULT false,
			department_name TEXT,
			dm_key TEXT,
			avatar_url TEXT NOT NULL DEFAULT '',
			is_archived BOOLEAN NOT NULL DEFAULT false,
			created_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			created_by_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			created_by_name TEXT NOT NULL DEFAULT 'System',
			last_message_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE UNIQUE INDEX IF NOT EXISTS chat_channels_org_name_idx ON chat_channels (organization_id, lower(name)) WHERE channel_type = 'channel';
		CREATE UNIQUE INDEX IF NOT EXISTS chat_channels_org_dm_key_idx ON chat_channels (organization_id, dm_key) WHERE dm_key IS NOT NULL;
		CREATE INDEX IF NOT EXISTS chat_channels_org_active_idx ON chat_channels (organization_id, is_archived, created_at ASC);

		CREATE TABLE IF NOT EXISTS chat_channel_members (
			channel_id UUID NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
			last_read_message_id UUID,
			last_read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			unread_count INTEGER NOT NULL DEFAULT 0 CHECK (unread_count >= 0),
			mention_count INTEGER NOT NULL DEFAULT 0 CHECK (mention_count >= 0),
			is_muted BOOLEAN NOT NULL DEFAULT false,
			notification_pref TEXT NOT NULL DEFAULT 'all' CHECK (notification_pref IN ('all', 'mentions', 'none')),
			joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (channel_id, employee_id)
		);
		CREATE INDEX IF NOT EXISTS chat_channel_members_emp_org_idx ON chat_channel_members (organization_id, employee_id);
		CREATE INDEX IF NOT EXISTS chat_channel_members_user_org_idx ON chat_channel_members (organization_id, user_id) WHERE user_id IS NOT NULL;

		CREATE TABLE IF NOT EXISTS chat_channel_join_requests (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			channel_id UUID NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
			reviewed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (channel_id, employee_id)
		);
		CREATE INDEX IF NOT EXISTS chat_join_requests_channel_pending_idx ON chat_channel_join_requests (channel_id, status) WHERE status = 'pending';

		CREATE TABLE IF NOT EXISTS chat_messages (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			channel_id UUID NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
			parent_message_id UUID REFERENCES chat_messages(id) ON DELETE CASCADE,
			sender_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			sender_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			sender_name TEXT NOT NULL CHECK (char_length(sender_name) BETWEEN 1 AND 200),
			sender_avatar TEXT NOT NULL DEFAULT '',
			sender_role TEXT NOT NULL DEFAULT 'Team Member',
			content TEXT NOT NULL DEFAULT '' CHECK (char_length(content) <= 20000),
			client_msg_id TEXT,
			is_pinned BOOLEAN NOT NULL DEFAULT false,
			pinned_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			pinned_at TIMESTAMPTZ,
			reply_count INTEGER NOT NULL DEFAULT 0 CHECK (reply_count >= 0),
			last_reply_at TIMESTAMPTZ,
			edited_at TIMESTAMPTZ,
			deleted_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			search_tsv TSVECTOR GENERATED ALWAYS AS (
				to_tsvector('english', coalesce(content, '') || ' ' || coalesce(sender_name, ''))
			) STORED,
			CHECK (parent_message_id IS NULL OR parent_message_id <> id)
		);
		CREATE UNIQUE INDEX IF NOT EXISTS chat_messages_idempotency_idx ON chat_messages (organization_id, channel_id, sender_employee_id, client_msg_id) WHERE client_msg_id IS NOT NULL AND sender_employee_id IS NOT NULL;
		CREATE INDEX IF NOT EXISTS chat_messages_channel_cursor_idx ON chat_messages (channel_id, created_at DESC, id DESC) WHERE parent_message_id IS NULL AND deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS chat_messages_thread_replies_idx ON chat_messages (parent_message_id, created_at ASC, id ASC) WHERE parent_message_id IS NOT NULL AND deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS chat_messages_channel_pinned_idx ON chat_messages (channel_id, created_at DESC) WHERE is_pinned = true AND deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS chat_messages_search_tsv_idx ON chat_messages USING GIN (search_tsv) WHERE deleted_at IS NULL;
		CREATE INDEX IF NOT EXISTS chat_messages_org_created_idx ON chat_messages (organization_id, created_at DESC, id DESC) WHERE deleted_at IS NULL;

		CREATE TABLE IF NOT EXISTS chat_message_reactions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			message_id UUID NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			emoji TEXT NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 32),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE (message_id, employee_id, emoji)
		);
		CREATE INDEX IF NOT EXISTS chat_reactions_message_idx ON chat_message_reactions (message_id);

		CREATE TABLE IF NOT EXISTS chat_message_saves (
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			message_id UUID NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (employee_id, message_id)
		);

		CREATE TABLE IF NOT EXISTS chat_attachments (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			channel_id UUID REFERENCES chat_channels(id) ON DELETE CASCADE,
			message_id UUID REFERENCES chat_messages(id) ON DELETE CASCADE,
			uploaded_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			uploaded_by_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			filename TEXT NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255),
			content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
			media_type TEXT NOT NULL DEFAULT 'file' CHECK (media_type IN ('image', 'video', 'file')),
			size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0 AND size_bytes <= 52428800),
			size_label TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL DEFAULT '',
			external_url TEXT NOT NULL DEFAULT '',
			inline_data BYTEA,
			sha256 TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS chat_attachments_message_idx ON chat_attachments (message_id) WHERE message_id IS NOT NULL;
		CREATE INDEX IF NOT EXISTS chat_attachments_org_idx ON chat_attachments (organization_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS chat_polls (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			message_id UUID NOT NULL UNIQUE REFERENCES chat_messages(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			question TEXT NOT NULL CHECK (char_length(question) BETWEEN 1 AND 500),
			allow_multiple BOOLEAN NOT NULL DEFAULT false,
			is_anonymous BOOLEAN NOT NULL DEFAULT false,
			is_closed BOOLEAN NOT NULL DEFAULT false,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);

		CREATE TABLE IF NOT EXISTS chat_poll_options (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			poll_id UUID NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
			option_text TEXT NOT NULL CHECK (char_length(option_text) BETWEEN 1 AND 300),
			sort_order SMALLINT NOT NULL DEFAULT 0
		);
		CREATE INDEX IF NOT EXISTS chat_poll_options_poll_idx ON chat_poll_options (poll_id, sort_order ASC);

		CREATE TABLE IF NOT EXISTS chat_poll_votes (
			poll_id UUID NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
			option_id UUID NOT NULL REFERENCES chat_poll_options(id) ON DELETE CASCADE,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (option_id, employee_id)
		);
		CREATE INDEX IF NOT EXISTS chat_poll_votes_poll_idx ON chat_poll_votes (poll_id);

		CREATE TABLE IF NOT EXISTS chat_mentions (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			message_id UUID NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
			channel_id UUID NOT NULL REFERENCES chat_channels(id) ON DELETE CASCADE,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			mentioned_employee_id UUID REFERENCES employees(id) ON DELETE CASCADE,
			mentioned_user_id UUID REFERENCES users(id) ON DELETE CASCADE,
			mention_type TEXT NOT NULL CHECK (mention_type IN ('user', 'channel', 'here')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS chat_mentions_user_idx ON chat_mentions (organization_id, mentioned_user_id, created_at DESC) WHERE mentioned_user_id IS NOT NULL;
		CREATE INDEX IF NOT EXISTS chat_mentions_message_idx ON chat_mentions (message_id);

		CREATE TABLE IF NOT EXISTS chat_user_presence (
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
			user_id UUID REFERENCES users(id) ON DELETE SET NULL,
			status TEXT NOT NULL DEFAULT 'online' CHECK (status IN ('online', 'away', 'dnd', 'offline')),
			custom_status TEXT NOT NULL DEFAULT '',
			dnd_until TIMESTAMPTZ,
			last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, employee_id)
		);
		CREATE INDEX IF NOT EXISTS chat_user_presence_org_status_idx ON chat_user_presence (organization_id, last_seen_at DESC);

		CREATE TABLE IF NOT EXISTS chat_events (
			id BIGSERIAL PRIMARY KEY,
			organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			channel_id UUID REFERENCES chat_channels(id) ON DELETE CASCADE,
			target_employee_id UUID REFERENCES employees(id) ON DELETE CASCADE,
			actor_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
			event_type TEXT NOT NULL,
			version SMALLINT NOT NULL DEFAULT 1,
			payload JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE INDEX IF NOT EXISTS chat_events_org_id_idx ON chat_events (organization_id, id ASC);
		CREATE INDEX IF NOT EXISTS chat_events_channel_id_idx ON chat_events (channel_id, id ASC) WHERE channel_id IS NOT NULL;
		CREATE INDEX IF NOT EXISTS chat_events_created_at_idx ON chat_events (created_at);
	`
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}

