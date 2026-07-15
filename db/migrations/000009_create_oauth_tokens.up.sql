CREATE TABLE IF NOT EXISTS oauth_tokens (
    id                 UUID        PRIMARY KEY,
    oauth_identity_id  UUID        NOT NULL REFERENCES oauth_identities(id) ON DELETE CASCADE,
    access_token_enc   BYTEA       NOT NULL,
    refresh_token_enc  BYTEA,
    scope              TEXT,
    expires_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (oauth_identity_id)
);
