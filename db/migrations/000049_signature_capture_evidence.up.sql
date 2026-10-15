-- Complete signature capture metadata and event history for electronic signing.
ALTER TABLE signature_recipients
    ADD COLUMN IF NOT EXISTS user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS signature_type TEXT,
    ADD COLUMN IF NOT EXISTS signature_data TEXT,
    ADD COLUMN IF NOT EXISTS signer_ip INET,
    ADD COLUMN IF NOT EXISTS signer_user_agent TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS consent_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS decline_reason TEXT,
    ADD COLUMN IF NOT EXISTS declined_at TIMESTAMPTZ;

ALTER TABLE signature_requests
    ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS signature_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    signature_request_id UUID NOT NULL REFERENCES signature_requests(id) ON DELETE CASCADE,
    recipient_id UUID REFERENCES signature_recipients(id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT '',
    ip_address INET,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS signature_events_request_idx
    ON signature_events (signature_request_id, created_at DESC);

CREATE INDEX IF NOT EXISTS signature_recipients_user_idx
    ON signature_recipients (user_id) WHERE user_id IS NOT NULL;
