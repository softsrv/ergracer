CREATE TABLE IF NOT EXISTS discord_guilds (
    id          UUID        PRIMARY KEY,
    guild_id    TEXT        NOT NULL UNIQUE,
    guild_name  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
