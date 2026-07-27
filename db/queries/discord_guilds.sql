-- name: UpsertDiscordGuild :one
INSERT INTO discord_guilds (id, guild_id, guild_name, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (guild_id) DO UPDATE
    SET guild_name = EXCLUDED.guild_name,
        updated_at = NOW()
RETURNING *;
