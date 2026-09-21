INSERT INTO permissions (key, description) VALUES
    ('scheduling:manage', 'Manage shift schedules and assignments'),
    ('scheduling:read', 'View shift schedules and assignments')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE shift_schedules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 255),
    timezone TEXT NOT NULL DEFAULT 'UTC',
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX shift_schedules_org_idx ON shift_schedules (organization_id);

CREATE TABLE shift_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    schedule_id UUID NOT NULL REFERENCES shift_schedules(id) ON DELETE CASCADE,
    day_of_week INTEGER NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    break_minutes INTEGER NOT NULL DEFAULT 0 CHECK (break_minutes >= 0),
    UNIQUE (schedule_id, day_of_week)
);

DROP TABLE IF EXISTS shift_assignments CASCADE;

CREATE TABLE shift_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE CASCADE,
    schedule_id UUID NOT NULL REFERENCES shift_schedules(id) ON DELETE RESTRICT,
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (effective_to IS NULL OR effective_from <= effective_to)
);

CREATE INDEX shift_assignments_emp_idx ON shift_assignments (employee_id, effective_from DESC);
