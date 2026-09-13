INSERT INTO permissions (key, description) VALUES
    ('documents:upload', 'Upload employee documents'),
    ('documents:read', 'Read employee documents')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    employee_id UUID REFERENCES employees(id) ON DELETE RESTRICT,
    filename TEXT NOT NULL CHECK (char_length(filename) BETWEEN 1 AND 255),
    category TEXT NOT NULL CHECK (category IN ('contract', 'identity', 'compliance', 'certification', 'other')),
    classification TEXT NOT NULL DEFAULT 'confidential' CHECK (classification IN ('internal', 'confidential', 'restricted')),
    content_type TEXT NOT NULL CHECK (char_length(content_type) BETWEEN 1 AND 120),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0 AND size_bytes <= 52428800),
    object_key TEXT NOT NULL UNIQUE,
    sha256 CHAR(64),
    status TEXT NOT NULL DEFAULT 'pending_upload' CHECK (status IN ('pending_upload', 'pending_scan', 'available', 'quarantined', 'deleted')),
    upload_expires_at TIMESTAMPTZ NOT NULL,
    expires_on DATE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    deleted_at TIMESTAMPTZ,
    deleted_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX documents_org_employee_idx ON documents (organization_id, employee_id, created_at DESC) WHERE status <> 'deleted';
CREATE INDEX documents_pending_upload_idx ON documents (upload_expires_at) WHERE status = 'pending_upload';

CREATE TABLE document_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE RESTRICT,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    object_key TEXT NOT NULL UNIQUE,
    sha256 CHAR(64),
    size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    UNIQUE (document_id, version_number)
);
