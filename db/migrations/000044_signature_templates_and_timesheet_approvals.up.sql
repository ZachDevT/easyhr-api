ALTER TABLE signature_requests ALTER COLUMN document_id DROP NOT NULL;
ALTER TABLE signature_requests DROP CONSTRAINT IF EXISTS signature_requests_status_check;
ALTER TABLE signature_requests ADD CONSTRAINT signature_requests_status_check CHECK (status IN ('draft','pending','sent','delivered','completed','declined','voided'));
ALTER TABLE signature_recipients DROP CONSTRAINT IF EXISTS signature_recipients_status_check;
ALTER TABLE signature_recipients ADD CONSTRAINT signature_recipients_status_check CHECK (status IN ('created','pending','sent','delivered','completed','declined'));
ALTER TABLE signature_requests ADD COLUMN IF NOT EXISTS public_token_hash CHAR(64);
ALTER TABLE signature_recipients ADD COLUMN IF NOT EXISTS public_token_hash CHAR(64);
CREATE UNIQUE INDEX IF NOT EXISTS signature_recipients_public_token_idx ON signature_recipients (public_token_hash) WHERE public_token_hash IS NOT NULL;

CREATE TABLE IF NOT EXISTS timesheet_submissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    week_start DATE NOT NULL,
    week_end DATE NOT NULL,
    total_minutes INTEGER NOT NULL DEFAULT 0 CHECK (total_minutes >= 0),
    status TEXT NOT NULL DEFAULT 'submitted' CHECK (status IN ('submitted','approved','rejected')),
    employee_note TEXT NOT NULL DEFAULT '',
    reviewer_note TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    reviewed_by_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (employee_id, week_start)
);
CREATE INDEX IF NOT EXISTS timesheet_submissions_org_status_idx ON timesheet_submissions (organization_id, status, week_start DESC);

INSERT INTO permissions (key, description) VALUES
    ('timesheet:approve', 'Review submitted timesheets'),
    ('timesheet:submit_self', 'Submit own weekly timesheet')
ON CONFLICT (key) DO NOTHING;
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, p.permission_key
FROM roles r CROSS JOIN (VALUES ('timesheet:approve'), ('timesheet:submit_self')) AS p(permission_key)
WHERE r.key = 'company_owner' OR r.key ILIKE '%admin%' OR r.key ILIKE '%hr%' OR r.key ILIKE '%manager%'
   OR r.display_name ILIKE '%owner%' OR r.display_name ILIKE '%admin%' OR r.display_name ILIKE '%hr%' OR r.display_name ILIKE '%manager%'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, 'timesheet:submit_self' FROM roles r WHERE r.key = 'employee' OR r.display_name ILIKE '%employee%'
ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS payroll_tax_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    country TEXT NOT NULL CHECK (char_length(country) = 2),
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    rule_name TEXT NOT NULL,
    rule_type TEXT NOT NULL CHECK (rule_type IN ('progressive_tax','flat_deduction')),
    bands JSONB NOT NULL DEFAULT '[]'::jsonb,
    rate_percent NUMERIC(7,4) NOT NULL DEFAULT 0 CHECK (rate_percent BETWEEN 0 AND 100),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, country, rule_name)
);

CREATE TABLE IF NOT EXISTS public_rate_limits (
    key_hash CHAR(64) NOT NULL,
    window_started TIMESTAMPTZ NOT NULL,
    request_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (key_hash, window_started)
);
CREATE INDEX IF NOT EXISTS public_rate_limits_expiry_idx ON public_rate_limits (window_started);
