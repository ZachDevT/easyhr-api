CREATE TABLE employees (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    user_id UUID UNIQUE REFERENCES users(id) ON DELETE RESTRICT,
    employee_number TEXT NOT NULL CHECK (char_length(employee_number) BETWEEN 1 AND 80),
    first_name TEXT NOT NULL CHECK (char_length(first_name) BETWEEN 1 AND 120),
    last_name TEXT NOT NULL CHECK (char_length(last_name) BETWEEN 1 AND 120),
    preferred_name TEXT,
    work_email TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('onboarding', 'active', 'leave', 'suspended', 'terminated')),
    hired_on DATE,
    terminated_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (terminated_on IS NULL OR hired_on IS NULL OR terminated_on >= hired_on),
    UNIQUE (organization_id, employee_number),
    UNIQUE (organization_id, work_email)
);

CREATE INDEX employees_organization_status_idx ON employees (organization_id, status, id);
CREATE INDEX employees_organization_name_idx ON employees (organization_id, last_name, first_name, id);

CREATE TABLE employee_personal_profiles (
    employee_id UUID PRIMARY KEY REFERENCES employees(id) ON DELETE RESTRICT,
    personal_email TEXT,
    personal_phone TEXT,
    birth_date DATE,
    gender TEXT,
    address_line_1 TEXT,
    address_line_2 TEXT,
    city TEXT,
    administrative_area TEXT,
    postal_code TEXT,
    country_code CHAR(2) CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE employment_terms (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    effective_from DATE NOT NULL,
    effective_to DATE,
    employment_type TEXT NOT NULL CHECK (employment_type IN ('full_time', 'part_time', 'fixed_term', 'contractor', 'intern')),
    worker_type TEXT NOT NULL CHECK (worker_type IN ('employee', 'contractor')),
    work_arrangement TEXT NOT NULL CHECK (work_arrangement IN ('on_site', 'hybrid', 'remote')),
    fte_basis_points INTEGER NOT NULL CHECK (fte_basis_points BETWEEN 1 AND 10000),
    pay_type TEXT NOT NULL CHECK (pay_type IN ('salary', 'hourly')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (effective_to IS NULL OR effective_to >= effective_from),
    UNIQUE (employee_id, effective_from)
);

CREATE INDEX employment_terms_active_idx ON employment_terms (employee_id, effective_from DESC) WHERE effective_to IS NULL;

CREATE TABLE employment_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    department_id UUID REFERENCES departments(id) ON DELETE RESTRICT,
    division_id UUID REFERENCES divisions(id) ON DELETE RESTRICT,
    location_id UUID REFERENCES locations(id) ON DELETE RESTRICT,
    manager_employee_id UUID REFERENCES employees(id) ON DELETE RESTRICT,
    job_title TEXT NOT NULL CHECK (char_length(job_title) BETWEEN 1 AND 160),
    effective_from DATE NOT NULL,
    effective_to DATE,
    is_primary BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT,
    CHECK (manager_employee_id IS NULL OR manager_employee_id <> employee_id),
    CHECK (effective_to IS NULL OR effective_to >= effective_from),
    UNIQUE (employee_id, effective_from, is_primary)
);

CREATE INDEX employment_assignments_active_idx ON employment_assignments (employee_id, effective_from DESC) WHERE effective_to IS NULL;
CREATE INDEX employment_assignments_manager_idx ON employment_assignments (manager_employee_id) WHERE manager_employee_id IS NOT NULL AND effective_to IS NULL;

CREATE TABLE employee_status_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    employee_id UUID NOT NULL REFERENCES employees(id) ON DELETE RESTRICT,
    previous_status TEXT,
    new_status TEXT NOT NULL CHECK (new_status IN ('onboarding', 'active', 'leave', 'suspended', 'terminated')),
    effective_on DATE NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX employee_status_events_employee_effective_idx ON employee_status_events (employee_id, effective_on DESC);
