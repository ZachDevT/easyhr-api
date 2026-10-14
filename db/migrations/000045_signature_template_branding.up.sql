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

ALTER TABLE signature_templates ADD COLUMN IF NOT EXISTS header_text TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_templates ADD COLUMN IF NOT EXISTS footer_text TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS header_rendered TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS footer_rendered TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS brand_color TEXT NOT NULL DEFAULT '#4F46E5';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS logo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS website_url TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS contact_phone TEXT NOT NULL DEFAULT '';
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS contact_email TEXT NOT NULL DEFAULT '';
