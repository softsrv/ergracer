CREATE TABLE IF NOT EXISTS discord_registrations (
    id                  UUID        PRIMARY KEY,
    discord_user_id     TEXT        NOT NULL,
    discord_username    TEXT        NOT NULL,
    guild_id            TEXT        NOT NULL,
    guild_name          TEXT        NOT NULL,
    user_id             UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (discord_user_id, guild_id)
);

CREATE INDEX IF NOT EXISTS idx_discord_registrations_user ON discord_registrations (user_id) WHERE user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_discord_registrations_discord_user ON discord_registrations (discord_user_id);
