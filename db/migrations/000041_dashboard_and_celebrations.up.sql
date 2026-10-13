-- Migration 000041: Dashboard Activities, Announcements, Celebrations, and Notifications

-- 1. Permissions
INSERT INTO permissions (key, description) VALUES
    ('attendance:manage_activities', 'Create and manage attendance punch activities'),
    ('announcements:manage', 'Create and delete company announcements'),
    ('celebrations:wish', 'Send celebration wishes to teammates')
ON CONFLICT (key) DO NOTHING;

-- 2. Attendance Activities
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

-- 3. Extend attendance_punches with activity and billable flag
ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS activity TEXT;
ALTER TABLE attendance_punches ADD COLUMN IF NOT EXISTS is_billable BOOLEAN DEFAULT true;

-- 4. Company Announcements
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

-- 5. Celebration Wishes
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

-- 6. User Notifications
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

-- 7. User Preferences (Dashboard widget customization)
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    active_widgets JSONB NOT NULL DEFAULT '["feed","birthdays"]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
