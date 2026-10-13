-- 000033_candidate_email_threads_and_macros.up.sql

-- 1. Macros table (email templates / canned responses with shortcut keywords, e.g. /interview, /rejection, /offer)
CREATE TABLE IF NOT EXISTS email_macros (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    title VARCHAR(255) NOT NULL,
    shortcut VARCHAR(100) NOT NULL, -- e.g. "interview-invite", "phone-screen", "rejection"
    category VARCHAR(100) NOT NULL DEFAULT 'hiring', -- hiring, general, onboarding
    subject VARCHAR(255) NOT NULL,
    body_html TEXT NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_email_macros_org ON email_macros(organization_id);
CREATE INDEX IF NOT EXISTS idx_email_macros_shortcut ON email_macros(organization_id, shortcut);

-- 2. Email threads table (represents an email conversation thread with a candidate or employee)
CREATE TABLE IF NOT EXISTS candidate_email_threads (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    applicant_id UUID NOT NULL REFERENCES job_applicants(id) ON DELETE CASCADE,
    subject VARCHAR(255) NOT NULL,
    candidate_email VARCHAR(255) NOT NULL,
    candidate_name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'open', -- open, replied, closed
    last_message_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_candidate_threads_applicant ON candidate_email_threads(applicant_id);
CREATE INDEX IF NOT EXISTS idx_candidate_threads_org ON candidate_email_threads(organization_id);

-- 3. Email messages table (individual inbound/outbound messages in a thread)
CREATE TABLE IF NOT EXISTS candidate_email_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    thread_id UUID NOT NULL REFERENCES candidate_email_threads(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sender_type VARCHAR(50) NOT NULL, -- 'recruiter', 'candidate'
    sender_name VARCHAR(255) NOT NULL,
    sender_email VARCHAR(255) NOT NULL,
    recipient_email VARCHAR(255) NOT NULL,
    subject VARCHAR(255) NOT NULL,
    body_html TEXT NOT NULL,
    resend_id VARCHAR(255),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_candidate_messages_thread ON candidate_email_messages(thread_id);
CREATE INDEX IF NOT EXISTS idx_candidate_messages_org ON candidate_email_messages(organization_id);
