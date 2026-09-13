INSERT INTO permissions (key, description) VALUES
    ('signatures:manage', 'Send and manage signature requests'),
    ('signatures:read', 'View signature requests')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE signature_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    provider TEXT NOT NULL CHECK (provider IN ('docusign')),
    provider_envelope_id TEXT,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'delivered', 'completed', 'declined', 'voided')),
    expires_at TIMESTAMPTZ,
    custom_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX signature_requests_org_status_idx ON signature_requests (organization_id, status);

CREATE TABLE signature_recipients (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    signature_request_id UUID NOT NULL REFERENCES signature_requests(id) ON DELETE CASCADE,
    employee_id UUID REFERENCES employees(id) ON DELETE SET NULL,
    recipient_email TEXT NOT NULL,
    recipient_name TEXT NOT NULL,
    signing_order INTEGER NOT NULL DEFAULT 1 CHECK (signing_order > 0),
    status TEXT NOT NULL DEFAULT 'created' CHECK (status IN ('created', 'sent', 'delivered', 'completed', 'declined')),
    viewed_at TIMESTAMPTZ,
    signed_at TIMESTAMPTZ,
    UNIQUE (signature_request_id, recipient_email)
);
