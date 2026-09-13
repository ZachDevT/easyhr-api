CREATE TABLE leave_types (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    code TEXT NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{1,62}$'),
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    unit TEXT NOT NULL CHECK (unit IN ('minutes', 'days')),
    is_paid BOOLEAN NOT NULL DEFAULT true,
    requires_approval BOOLEAN NOT NULL DEFAULT true,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, code),
    UNIQUE (organization_id, name)
);

CREATE TABLE leave_policies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    leave_type_id UUID NOT NULL REFERENCES leave_types(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    accrual_frequency TEXT NOT NULL CHECK (accrual_frequency IN ('none', 'weekly', 'monthly', 'annual')),
    accrual_minutes INTEGER NOT NULL DEFAULT 0 CHECK (accrual_minutes >= 0),
    carryover_limit_minutes INTEGER CHECK (carryover_limit_minutes IS NULL OR carryover_limit_minutes >= 0),
    allows_negative_balance BOOLEAN NOT NULL DEFAULT false,
    effective_from DATE NOT NULL,
    effective_to DATE,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('draft', 'active', 'retired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (effective_to IS NULL OR effective_to >= effective_from),
    UNIQUE (organization_id, name, effective_from)
);

CREATE INDEX leave_policies_active_idx ON leave_policies (organization_id, leave_type_id, effective_from DESC) WHERE status = 'active';

CREATE TABLE leave_policy_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    leave_policy_id UUID NOT NULL REFERENCES leave_policies(id) ON DELETE RESTRICT,
    effective_from DATE NOT NULL,
    effective_to DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (effective_to IS NULL OR effective_to >= effective_from),
    UNIQUE (employee_id, leave_policy_id, effective_from)
);

CREATE INDEX leave_policy_assignments_active_idx ON leave_policy_assignments (employee_id, effective_from DESC) WHERE effective_to IS NULL;

CREATE TABLE leave_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    leave_type_id UUID NOT NULL REFERENCES leave_types(id) ON DELETE RESTRICT,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    requested_minutes INTEGER NOT NULL CHECK (requested_minutes > 0),
    reason TEXT,
    status TEXT NOT NULL CHECK (status IN ('draft', 'pending_manager', 'pending_hr', 'approved', 'declined', 'cancelled')),
    submitted_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    cancelled_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_date >= start_date),
    CHECK ((status = 'draft' AND submitted_at IS NULL) OR (status <> 'draft' AND submitted_at IS NOT NULL))
);

CREATE INDEX leave_requests_employee_dates_idx ON leave_requests (employee_id, start_date, end_date);
CREATE INDEX leave_requests_approval_idx ON leave_requests (organization_id, status, submitted_at) WHERE status IN ('pending_manager', 'pending_hr');

CREATE TABLE leave_request_decisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    leave_request_id UUID NOT NULL REFERENCES leave_requests(id) ON DELETE RESTRICT,
    stage TEXT NOT NULL CHECK (stage IN ('manager', 'hr')),
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'declined')),
    decided_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    note TEXT,
    decided_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (leave_request_id, stage)
);

CREATE TABLE leave_ledger_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    leave_type_id UUID NOT NULL REFERENCES leave_types(id) ON DELETE RESTRICT,
    entry_type TEXT NOT NULL CHECK (entry_type IN ('accrual', 'adjustment', 'reservation', 'release', 'usage', 'carryover', 'expiry')),
    minutes INTEGER NOT NULL CHECK (minutes <> 0),
    effective_on DATE NOT NULL,
    source_type TEXT NOT NULL,
    source_id UUID,
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE NULLS NOT DISTINCT (source_type, source_id, entry_type)
);

CREATE INDEX leave_ledger_balance_idx ON leave_ledger_entries (employee_id, leave_type_id, effective_on, created_at);
