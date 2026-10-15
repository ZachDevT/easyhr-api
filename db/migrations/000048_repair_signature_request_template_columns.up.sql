-- Repair installations where migration 45 was recorded as applied without
-- creating every column now used by the signature template workflow.
ALTER TABLE signature_requests
    ADD COLUMN IF NOT EXISTS template_id UUID REFERENCES signature_templates(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body_rendered TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS header_rendered TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS footer_rendered TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS body_sha256 CHAR(64) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS brand_color TEXT NOT NULL DEFAULT '#4F46E5',
    ADD COLUMN IF NOT EXISTS logo_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS website_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contact_phone TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS contact_email TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

ALTER TABLE signature_requests DROP CONSTRAINT IF EXISTS signature_requests_provider_check;
ALTER TABLE signature_requests ADD CONSTRAINT signature_requests_provider_check
    CHECK (provider IN ('docusign', 'native'));

ALTER TABLE signature_requests DROP CONSTRAINT IF EXISTS signature_requests_status_check;
ALTER TABLE signature_requests ADD CONSTRAINT signature_requests_status_check
    CHECK (status IN ('draft', 'pending', 'sent', 'delivered', 'completed', 'declined', 'voided'));

ALTER TABLE signature_recipients DROP CONSTRAINT IF EXISTS signature_recipients_status_check;
ALTER TABLE signature_recipients ADD CONSTRAINT signature_recipients_status_check
    CHECK (status IN ('created', 'pending', 'sent', 'delivered', 'completed', 'declined'));

CREATE INDEX IF NOT EXISTS signature_requests_template_idx
    ON signature_requests (template_id) WHERE template_id IS NOT NULL;
