CREATE TABLE platform_operators (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    mfa_enabled BOOLEAN NOT NULL DEFAULT false,
    mfa_secret TEXT,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE platform_roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT UNIQUE NOT NULL,
    description TEXT
);

CREATE TABLE platform_permissions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT UNIQUE NOT NULL,
    description TEXT
);

INSERT INTO platform_permissions (key, description) VALUES
    ('platform:super_admin', 'Full God-mode access'),
    ('platform:manage_billing', 'Manage subscriptions and billing'),
    ('platform:support', 'Access support tickets and request tenant sessions');

CREATE TABLE platform_role_permissions (
    role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    permission_id UUID NOT NULL REFERENCES platform_permissions(id) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_id)
);

CREATE TABLE platform_operator_roles (
    operator_id UUID NOT NULL REFERENCES platform_operators(id) ON DELETE CASCADE,
    role_id UUID NOT NULL REFERENCES platform_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (operator_id, role_id)
);

CREATE TABLE support_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_id UUID NOT NULL REFERENCES platform_operators(id) ON DELETE RESTRICT,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX support_sessions_org_idx ON support_sessions (organization_id);
CREATE INDEX support_sessions_operator_idx ON support_sessions (operator_id);
