CREATE TABLE IF NOT EXISTS organization_onboarding_settings (
    organization_id UUID PRIMARY KEY REFERENCES organizations(id) ON DELETE CASCADE,
    welcome_title TEXT NOT NULL DEFAULT 'Welcome to the Team',
    welcome_message TEXT NOT NULL DEFAULT 'We are excited to have you join our team. Let us get your profile set up.',
    welcome_video_url TEXT DEFAULT '',
    welcome_video_required BOOLEAN NOT NULL DEFAULT false,
    require_bio_and_avatar BOOLEAN NOT NULL DEFAULT true,
    require_culture_favorites BOOLEAN NOT NULL DEFAULT true,
    require_direct_deposit BOOLEAN NOT NULL DEFAULT true,
    require_handbook BOOLEAN NOT NULL DEFAULT true,
    handbook_title TEXT NOT NULL DEFAULT 'Employee Handbook & Code of Conduct',
    handbook_url TEXT DEFAULT '',
    handbook_content TEXT DEFAULT 'Welcome to our organization! As a member of our team, we uphold high standards of integrity, excellence, and mutual respect. Please review and acknowledge our policies.',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
