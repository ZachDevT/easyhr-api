CREATE TABLE tenant_data_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    job_type TEXT NOT NULL CHECK (job_type IN ('import_employees', 'import_payroll', 'export_full')),
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'validated', 'processing', 'complete', 'failed')),
    file_url TEXT,
    requested_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX tenant_data_jobs_org_idx ON tenant_data_jobs(organization_id);

CREATE TABLE tenant_compliance_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    request_type TEXT NOT NULL CHECK (request_type IN ('deletion', 'export')),
    subject_email TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'review', 'complete', 'rejected')),
    resolution_notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX tenant_compliance_requests_org_idx ON tenant_compliance_requests(organization_id);

CREATE TABLE platform_feature_flags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT UNIQUE NOT NULL,
    description TEXT,
    default_enabled BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tenant_feature_flags (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    feature_flag_id UUID NOT NULL REFERENCES platform_feature_flags(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by_operator_id UUID REFERENCES platform_operators(id) ON DELETE SET NULL,
    PRIMARY KEY (organization_id, feature_flag_id)
);

INSERT INTO platform_feature_flags (key, description, default_enabled) VALUES
    ('payroll_beta', 'Early access to payroll features', false),
    ('candidate_import_v2', 'New CSV import logic for candidates', false),
    ('global_time_off', 'Advanced multi-region time off rules', true);
