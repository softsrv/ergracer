CREATE TABLE IF NOT EXISTS discord_guild_settings (
    id                 UUID        PRIMARY KEY,
    guild_id           TEXT        NOT NULL UNIQUE,
    report_channel_id  TEXT        NOT NULL,
    set_by_user_id     TEXT        NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
