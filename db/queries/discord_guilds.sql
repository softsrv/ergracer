-- name: UpsertDiscordGuild :one
INSERT INTO discord_guilds (id, guild_id, guild_name, created_at, updated_at)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (guild_id) DO UPDATE
    SET guild_name = EXCLUDED.guild_name,
        updated_at = NOW()
RETURNING *;

-- name: ListDiscordGuilds :many
SELECT * FROM discord_guilds;

-- name: GetDiscordGuildByGuildID :one
SELECT * FROM discord_guilds WHERE guild_id = $1 LIMIT 1;
