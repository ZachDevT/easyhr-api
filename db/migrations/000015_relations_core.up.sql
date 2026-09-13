INSERT INTO permissions (key, description) VALUES
    ('relations:manage', 'Manage employee relations and disciplinary actions'),
    ('relations:read', 'View employee relation cases')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE relation_cases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    category TEXT NOT NULL CHECK (category IN ('attendance', 'conduct', 'performance', 'policy_violation', 'other')),
    severity TEXT NOT NULL CHECK (severity IN ('low', 'medium', 'high', 'critical')),
    status TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'under_review', 'closed')),
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 255),
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX relation_cases_org_employee_idx ON relation_cases (organization_id, employee_id, status);

CREATE TABLE relation_actions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    case_id UUID NOT NULL REFERENCES relation_cases(id) ON DELETE CASCADE,
    action_type TEXT NOT NULL CHECK (action_type IN ('warning', 'suspension', 'termination', 'note')),
    effective_date DATE NOT NULL,
    paid_suspension BOOLEAN,
    severance_details TEXT,
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 5000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX relation_actions_case_idx ON relation_actions (case_id, created_at DESC);
