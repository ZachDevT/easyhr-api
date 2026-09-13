CREATE TABLE organization_legal_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE RESTRICT,
    legal_name TEXT NOT NULL CHECK (char_length(legal_name) BETWEEN 1 AND 200),
    registration_number TEXT,
    tax_identifier TEXT,
    country_code CHAR(2) NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    industry TEXT,
    company_size_band TEXT,
    website_url TEXT,
    contact_email TEXT,
    contact_phone TEXT,
    address_line_1 TEXT,
    address_line_2 TEXT,
    city TEXT,
    administrative_area TEXT,
    postal_code TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE workspaces (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL UNIQUE REFERENCES organizations(id) ON DELETE RESTRICT,
    subdomain TEXT NOT NULL UNIQUE CHECK (subdomain ~ '^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$'),
    brand_color CHAR(7) NOT NULL DEFAULT '#A238FF' CHECK (brand_color ~ '^#[0-9A-Fa-f]{6}$'),
    logo_document_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE organization_social_links (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    platform TEXT NOT NULL CHECK (platform IN ('linkedin', 'x', 'facebook', 'tiktok', 'glassdoor')),
    url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, platform)
);

CREATE TABLE divisions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    code TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (organization_id, code),
    UNIQUE (organization_id, name)
);

CREATE TABLE departments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    division_id UUID REFERENCES divisions(id) ON DELETE RESTRICT,
    parent_department_id UUID REFERENCES departments(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    code TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE NULLS NOT DISTINCT (organization_id, code),
    UNIQUE (organization_id, name)
);

CREATE INDEX departments_parent_department_idx ON departments (parent_department_id) WHERE parent_department_id IS NOT NULL;

CREATE TABLE locations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 160),
    country_code CHAR(2) NOT NULL CHECK (country_code ~ '^[A-Z]{2}$'),
    timezone TEXT NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 100),
    address_line_1 TEXT,
    address_line_2 TEXT,
    city TEXT,
    administrative_area TEXT,
    postal_code TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE geofence_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    location_id UUID NOT NULL UNIQUE REFERENCES locations(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT false,
    latitude NUMERIC(9, 6),
    longitude NUMERIC(9, 6),
    radius_meters INTEGER,
    accuracy_tolerance_meters INTEGER NOT NULL DEFAULT 50 CHECK (accuracy_tolerance_meters BETWEEN 0 AND 1000),
    effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    effective_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (
        (enabled = false AND latitude IS NULL AND longitude IS NULL AND radius_meters IS NULL)
        OR
        (enabled = true
            AND latitude BETWEEN -90 AND 90
            AND longitude BETWEEN -180 AND 180
            AND radius_meters BETWEEN 10 AND 100000
            AND (effective_to IS NULL OR effective_to > effective_from))
    )
);
