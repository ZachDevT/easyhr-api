INSERT INTO permissions (key, description) VALUES
    ('payroll:manage', 'Manage payroll configurations and runs'),
    ('compensation:read', 'View employee compensation details'),
    ('compensation:manage', 'Manage employee compensation details')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE compensation_bands (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    min_minor_units BIGINT NOT NULL CHECK (min_minor_units >= 0),
    max_minor_units BIGINT NOT NULL CHECK (max_minor_units >= min_minor_units),
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE payroll_configurations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    country TEXT NOT NULL CHECK (char_length(country) = 2),
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    standard_monthly_minutes INTEGER NOT NULL DEFAULT 9600 CHECK (standard_monthly_minutes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, country)
);

CREATE TABLE payroll_periods (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    start_date DATE NOT NULL,
    end_date DATE NOT NULL,
    pay_date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'processing', 'review', 'locked', 'paid')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (end_date >= start_date)
);

CREATE TABLE payroll_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payroll_period_id UUID NOT NULL REFERENCES payroll_periods(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'calculated', 'approved')),
    approved_at TIMESTAMPTZ,
    approved_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE TABLE payroll_run_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payroll_run_id UUID NOT NULL REFERENCES payroll_runs(id) ON DELETE CASCADE,
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    base_pay BIGINT NOT NULL DEFAULT 0,
    earnings BIGINT NOT NULL DEFAULT 0,
    deductions BIGINT NOT NULL DEFAULT 0,
    net_pay BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payroll_run_id, employee_id)
);

CREATE TABLE paystubs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payroll_run_item_id UUID NOT NULL REFERENCES payroll_run_items(id) ON DELETE RESTRICT,
    document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    delivery_status TEXT NOT NULL DEFAULT 'manual' CHECK (delivery_status IN ('pending', 'delivered', 'manual')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (payroll_run_item_id)
);
