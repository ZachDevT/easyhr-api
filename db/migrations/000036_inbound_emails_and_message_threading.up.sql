-- 000036_inbound_emails_and_message_threading.up.sql

ALTER TABLE candidate_email_messages
    ADD COLUMN IF NOT EXISTS message_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS in_reply_to VARCHAR(255),
    ADD COLUMN IF NOT EXISTS raw_json JSONB;

CREATE INDEX IF NOT EXISTS idx_candidate_messages_msg_id ON candidate_email_messages(message_id);
CREATE INDEX IF NOT EXISTS idx_candidate_messages_resend_id ON candidate_email_messages(resend_id);

CREATE TABLE IF NOT EXISTS inbound_emails (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    resend_email_id VARCHAR(255) NOT NULL,
    sender_email VARCHAR(255) NOT NULL,
    sender_name VARCHAR(255),
    recipient_email VARCHAR(255) NOT NULL,
    subject TEXT,
    message_id VARCHAR(255),
    in_reply_to VARCHAR(255),
    body_html TEXT,
    body_text TEXT,
    raw_payload JSONB,
    status VARCHAR(50) NOT NULL DEFAULT 'received',
    matched_thread_id UUID REFERENCES candidate_email_threads(id) ON DELETE SET NULL,
    organization_id UUID REFERENCES organizations(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_inbound_emails_resend_id ON inbound_emails(resend_email_id);
CREATE INDEX IF NOT EXISTS idx_inbound_emails_sender ON inbound_emails(sender_email);
CREATE INDEX IF NOT EXISTS idx_inbound_emails_org ON inbound_emails(organization_id);
CREATE INDEX IF NOT EXISTS idx_inbound_emails_thread ON inbound_emails(matched_thread_id);
