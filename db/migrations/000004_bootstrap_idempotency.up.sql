CREATE TABLE bootstrap_idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    external_identity_id TEXT NOT NULL,
    route TEXT NOT NULL,
    key TEXT NOT NULL CHECK (char_length(key) BETWEEN 16 AND 255),
    request_hash TEXT NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE (external_identity_id, route, key)
);

CREATE INDEX bootstrap_idempotency_keys_expiry_idx ON bootstrap_idempotency_keys (expires_at);
