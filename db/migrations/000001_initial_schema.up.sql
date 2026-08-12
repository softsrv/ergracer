CREATE TABLE IF NOT EXISTS users (
    id                    UUID        PRIMARY KEY,
    email                 TEXT        NOT NULL UNIQUE,
    email_verified        BOOLEAN     NOT NULL DEFAULT false,
    setup_progress        INTEGER     NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_users_email ON users (email);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id            UUID        PRIMARY KEY,
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash    TEXT        NOT NULL,
    device_name   TEXT,
    ip_address    INET,
    user_agent    TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_refresh_tokens_token_hash ON refresh_tokens (token_hash);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id    ON refresh_tokens (user_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_expires_at ON refresh_tokens (expires_at);

CREATE TABLE IF NOT EXISTS email_verification_codes (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    used_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_evc_user_id    ON email_verification_codes (user_id);
CREATE INDEX IF NOT EXISTS idx_evc_expires_at ON email_verification_codes (expires_at);

CREATE TABLE IF NOT EXISTS oauth_identities (
    id                 UUID        PRIMARY KEY,
    user_id            UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider           TEXT        NOT NULL,
    provider_user_id   TEXT        NOT NULL,
    provider_username  TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (provider, provider_user_id),
    UNIQUE (user_id, provider)
);

CREATE INDEX IF NOT EXISTS idx_oauth_identities_user ON oauth_identities (user_id);

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

CREATE TABLE IF NOT EXISTS discord_registrations (
    id                UUID        PRIMARY KEY,
    discord_user_id   TEXT        NOT NULL,
    discord_username  TEXT        NOT NULL,
    guild_id          TEXT        NOT NULL,
    guild_name        TEXT        NOT NULL,
    user_id           UUID        REFERENCES users(id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (discord_user_id, guild_id)
);

CREATE INDEX IF NOT EXISTS idx_discord_registrations_user ON discord_registrations (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_discord_registrations_discord_user ON discord_registrations (discord_user_id);

CREATE TABLE IF NOT EXISTS discord_guild_settings (
    id                 UUID        PRIMARY KEY,
    guild_id           TEXT        NOT NULL UNIQUE,
    report_channel_id  TEXT        NOT NULL,
    channel_name       TEXT        NOT NULL DEFAULT '',
    set_by_user_id     TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS discord_guilds (
    id          UUID        PRIMARY KEY,
    guild_id    TEXT        NOT NULL UNIQUE,
    guild_name  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
