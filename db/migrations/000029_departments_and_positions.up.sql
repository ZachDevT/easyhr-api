ALTER TABLE departments 
ADD COLUMN IF NOT EXISTS description TEXT,
ADD COLUMN IF NOT EXISTS color TEXT DEFAULT '#1A73E8',
ADD COLUMN IF NOT EXISTS head_employee_id UUID REFERENCES employees(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS department_positions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    department_id UUID NOT NULL REFERENCES departments(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 160),
    level TEXT NOT NULL DEFAULT 'Mid',
    reports_to TEXT,
    pay_type TEXT NOT NULL DEFAULT 'Salaried',
    currency TEXT NOT NULL DEFAULT 'CAD',
    min_pay BIGINT NOT NULL DEFAULT 0,
    max_pay BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS department_positions_org_dept_idx ON department_positions (organization_id, department_id);

ALTER TABLE job_postings
ADD COLUMN IF NOT EXISTS department_id UUID REFERENCES departments(id) ON DELETE SET NULL,
ADD COLUMN IF NOT EXISTS position_id UUID REFERENCES department_positions(id) ON DELETE SET NULL;
