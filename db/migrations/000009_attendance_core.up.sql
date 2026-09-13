INSERT INTO permissions (key, description) VALUES
    ('attendance:punch_self', 'Clock in, clock out and record own breaks'),
    ('attendance:read_team', 'View attendance in permitted team scope'),
    ('attendance:correct', 'Create audited attendance corrections'),
    ('attendance:manage_shifts', 'Create and assign work shifts')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE shift_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    location_id UUID REFERENCES locations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    start_local_time TIME NOT NULL,
    end_local_time TIME NOT NULL,
    crosses_midnight BOOLEAN NOT NULL DEFAULT false,
    unpaid_break_minutes INTEGER NOT NULL DEFAULT 0 CHECK (unpaid_break_minutes BETWEEN 0 AND 720),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE (organization_id, name)
);

CREATE TABLE shift_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    shift_template_id UUID NOT NULL REFERENCES shift_templates(id) ON DELETE RESTRICT,
    work_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'cancelled')),
    published_at TIMESTAMPTZ,
    published_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE (employee_id, work_date)
);

CREATE INDEX shift_assignments_org_date_idx ON shift_assignments (organization_id, work_date, status);

CREATE TABLE attendance_punches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (kind IN ('clock_in', 'clock_out', 'break_start', 'break_end')),
    occurred_at TIMESTAMPTZ NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source TEXT NOT NULL CHECK (source IN ('web', 'mobile', 'kiosk', 'manager_override')),
    device_id TEXT,
    latitude NUMERIC(9,6),
    longitude NUMERIC(9,6),
    location_accuracy_meters INTEGER,
    policy_outcome TEXT NOT NULL DEFAULT 'not_evaluated' CHECK (policy_outcome IN ('not_evaluated', 'allowed', 'outside_geofence', 'needs_review')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK ((latitude IS NULL AND longitude IS NULL) OR (latitude BETWEEN -90 AND 90 AND longitude BETWEEN -180 AND 180)),
    CHECK (location_accuracy_meters IS NULL OR location_accuracy_meters >= 0)
);

CREATE INDEX attendance_punches_employee_occurred_idx ON attendance_punches (employee_id, occurred_at);
CREATE INDEX attendance_punches_org_occurred_idx ON attendance_punches (organization_id, occurred_at);

CREATE TABLE attendance_corrections (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    original_punch_id UUID NOT NULL REFERENCES attendance_punches(id) ON DELETE RESTRICT,
    replacement_occurred_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL CHECK (char_length(reason) BETWEEN 1 AND 2000),
    status TEXT NOT NULL DEFAULT 'applied' CHECK (status IN ('applied', 'reversed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX attendance_corrections_original_idx ON attendance_corrections (original_punch_id, created_at DESC);
