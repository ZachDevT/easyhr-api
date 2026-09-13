INSERT INTO permissions (key, description) VALUES
    ('promotions:manage', 'Execute and manage employee promotions'),
    ('promotions:read', 'View employee promotions')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE promotion_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    effective_date DATE NOT NULL,
    previous_job_title TEXT,
    new_job_title TEXT NOT NULL,
    previous_manager_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    new_manager_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    previous_compensation_minor BIGINT,
    new_compensation_minor BIGINT,
    rationale TEXT,
    status TEXT NOT NULL DEFAULT 'executed' CHECK (status IN ('executed', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX promotion_events_emp_idx ON promotion_events (employee_id, created_at DESC);
