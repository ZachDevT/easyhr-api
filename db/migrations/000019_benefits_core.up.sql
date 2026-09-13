INSERT INTO permissions (key, description) VALUES
    ('benefits:manage', 'Manage benefit plans and enrollments'),
    ('benefits:read', 'View benefit plans and enrollments')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE benefit_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    category TEXT NOT NULL CHECK (category IN ('health', 'retirement', 'transit', 'allowance', 'other')),
    description TEXT,
    employer_cost_minor BIGINT NOT NULL DEFAULT 0,
    employee_cost_minor BIGINT NOT NULL DEFAULT 0,
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX benefit_plans_org_idx ON benefit_plans (organization_id);

CREATE TABLE benefit_enrollments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    plan_id UUID NOT NULL REFERENCES benefit_plans(id) ON DELETE RESTRICT,
    coverage_tier TEXT NOT NULL CHECK (coverage_tier IN ('individual', 'family')),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    effective_from DATE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX benefit_enrollments_emp_idx ON benefit_enrollments (employee_id, status);

CREATE TABLE reward_statements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX reward_statements_emp_idx ON reward_statements (employee_id, created_at DESC);
