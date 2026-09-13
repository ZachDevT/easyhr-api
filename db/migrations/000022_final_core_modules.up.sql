INSERT INTO permissions (key, description) VALUES
    ('holidays:manage', 'Manage company holidays'),
    ('offboarding:manage', 'Manage employee offboarding plans'),
    ('policies:manage', 'Manage company policies')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE company_holidays (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    holiday_date DATE NOT NULL,
    holiday_type TEXT NOT NULL DEFAULT 'company' CHECK (holiday_type IN ('national', 'company')),
    applies_to TEXT NOT NULL DEFAULT 'all_employees',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);
CREATE INDEX company_holidays_org_idx ON company_holidays (organization_id, holiday_date);

CREATE TABLE offboarding_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    owner_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    last_working_day DATE NOT NULL,
    reason TEXT NOT NULL,
    risk_status TEXT NOT NULL DEFAULT 'on_track' CHECK (risk_status IN ('on_track', 'at_risk', 'complete')),
    progress_percentage INTEGER NOT NULL DEFAULT 0 CHECK (progress_percentage BETWEEN 0 AND 100),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);
CREATE INDEX offboarding_plans_org_idx ON offboarding_plans (organization_id);

CREATE TABLE company_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 255),
    category TEXT NOT NULL,
    requires_signature BOOLEAN NOT NULL DEFAULT false,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);
CREATE INDEX company_policies_org_idx ON company_policies (organization_id);
