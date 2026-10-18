-- Migration 000050: Workspace Chat Core (Channels, DMs, Messages, Threads, Reactions, Polls, Attachments, Presence, Real-time Events)

-- 1. Permissions for workspace chat
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

-- 2. Chat Channels (Public channels, Private channels, Department channels, 1:1 DMs, Group DMs)
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

CREATE UNIQUE INDEX IF NOT EXISTS chat_channels_org_name_idx
    ON chat_channels (organization_id, lower(name))
    WHERE channel_type = 'channel';

CREATE UNIQUE INDEX IF NOT EXISTS chat_channels_org_dm_key_idx
    ON chat_channels (organization_id, dm_key)
    WHERE dm_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_channels_org_active_idx
    ON chat_channels (organization_id, is_archived, created_at ASC);

-- 3. Channel Memberships & Read State Tracking
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

CREATE INDEX IF NOT EXISTS chat_channel_members_emp_org_idx
    ON chat_channel_members (organization_id, employee_id);

CREATE INDEX IF NOT EXISTS chat_channel_members_user_org_idx
    ON chat_channel_members (organization_id, user_id)
    WHERE user_id IS NOT NULL;

-- 4. Private / Department Channel Join Requests
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

CREATE INDEX IF NOT EXISTS chat_join_requests_channel_pending_idx
    ON chat_channel_join_requests (channel_id, status)
    WHERE status = 'pending';

-- 5. Chat Messages & Threaded Replies
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

CREATE UNIQUE INDEX IF NOT EXISTS chat_messages_idempotency_idx
    ON chat_messages (organization_id, channel_id, sender_employee_id, client_msg_id)
    WHERE client_msg_id IS NOT NULL AND sender_employee_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_messages_channel_cursor_idx
    ON chat_messages (channel_id, created_at DESC, id DESC)
    WHERE parent_message_id IS NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS chat_messages_thread_replies_idx
    ON chat_messages (parent_message_id, created_at ASC, id ASC)
    WHERE parent_message_id IS NOT NULL AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS chat_messages_channel_pinned_idx
    ON chat_messages (channel_id, created_at DESC)
    WHERE is_pinned = true AND deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS chat_messages_search_tsv_idx
    ON chat_messages USING GIN (search_tsv)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS chat_messages_org_created_idx
    ON chat_messages (organization_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

-- 6. Emoji Reactions
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

CREATE INDEX IF NOT EXISTS chat_reactions_message_idx
    ON chat_message_reactions (message_id);

-- 7. Saved / Bookmarked Messages
CREATE TABLE IF NOT EXISTS chat_message_saves (
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    message_id UUID NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (employee_id, message_id)
);

-- 8. File & Media Attachments
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

CREATE INDEX IF NOT EXISTS chat_attachments_message_idx
    ON chat_attachments (message_id)
    WHERE message_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_attachments_org_idx
    ON chat_attachments (organization_id, created_at DESC);

-- 9. Interactive Polls
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

CREATE INDEX IF NOT EXISTS chat_poll_options_poll_idx
    ON chat_poll_options (poll_id, sort_order ASC);

CREATE TABLE IF NOT EXISTS chat_poll_votes (
    poll_id UUID NOT NULL REFERENCES chat_polls(id) ON DELETE CASCADE,
    option_id UUID NOT NULL REFERENCES chat_poll_options(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (option_id, employee_id)
);

CREATE INDEX IF NOT EXISTS chat_poll_votes_poll_idx
    ON chat_poll_votes (poll_id);

-- 10. Mentions (@user, @channel, @here)
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

CREATE INDEX IF NOT EXISTS chat_mentions_user_idx
    ON chat_mentions (organization_id, mentioned_user_id, created_at DESC)
    WHERE mentioned_user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_mentions_message_idx
    ON chat_mentions (message_id);

-- 11. User Presence & Do-Not-Disturb
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

CREATE INDEX IF NOT EXISTS chat_user_presence_org_status_idx
    ON chat_user_presence (organization_id, last_seen_at DESC);

-- 12. Real-time Chat Events Outbox / Replay Log
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

CREATE INDEX IF NOT EXISTS chat_events_org_id_idx
    ON chat_events (organization_id, id ASC);

CREATE INDEX IF NOT EXISTS chat_events_channel_id_idx
    ON chat_events (channel_id, id ASC)
    WHERE channel_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS chat_events_created_at_idx
    ON chat_events (created_at);
