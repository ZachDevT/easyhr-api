INSERT INTO permissions (key, description) VALUES
    ('hiring:manage', 'Manage job postings and candidates'),
    ('hiring:read', 'View job postings and candidates')
ON CONFLICT (key) DO NOTHING;

CREATE TABLE job_postings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 255),
    description TEXT NOT NULL,
    location TEXT NOT NULL,
    employment_type TEXT NOT NULL CHECK (employment_type IN ('full_time', 'part_time', 'contract', 'internship')),
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'published', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);

CREATE INDEX job_postings_org_status_idx ON job_postings (organization_id, status);

CREATE TABLE job_applicants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_posting_id UUID NOT NULL REFERENCES job_postings(id) ON DELETE CASCADE,
    first_name TEXT NOT NULL,
    last_name TEXT NOT NULL,
    email TEXT NOT NULL,
    phone TEXT,
    resume_document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'screening', 'interviewing', 'offered', 'hired', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX job_applicants_posting_idx ON job_applicants (job_posting_id, created_at DESC);

CREATE TABLE job_interviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_applicant_id UUID NOT NULL REFERENCES job_applicants(id) ON DELETE CASCADE,
    interviewer_user_id UUID NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    scheduled_at TIMESTAMPTZ NOT NULL,
    duration_minutes INTEGER NOT NULL DEFAULT 60 CHECK (duration_minutes > 0),
    status TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'completed', 'cancelled')),
    meeting_link TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE job_offers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_applicant_id UUID NOT NULL UNIQUE REFERENCES job_applicants(id) ON DELETE CASCADE,
    base_amount_minor BIGINT NOT NULL CHECK (base_amount_minor >= 0),
    currency TEXT NOT NULL CHECK (char_length(currency) = 3),
    document_id UUID REFERENCES documents(id) ON DELETE SET NULL,
    signature_request_id UUID REFERENCES signature_requests(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'accepted', 'declined')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by_user_id UUID REFERENCES users(id) ON DELETE RESTRICT
);
