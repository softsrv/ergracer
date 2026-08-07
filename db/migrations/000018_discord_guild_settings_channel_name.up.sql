ALTER TABLE discord_guild_settings ADD COLUMN IF NOT EXISTS channel_name TEXT NOT NULL DEFAULT '';
