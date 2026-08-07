-- name: UpsertDiscordRegistration :one
INSERT INTO discord_registrations (id, discord_user_id, discord_username, guild_id, guild_name, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
ON CONFLICT (discord_user_id, guild_id) DO UPDATE
    SET discord_username = EXCLUDED.discord_username,
        guild_name       = EXCLUDED.guild_name,
        updated_at       = NOW()
RETURNING *;

-- name: GetDiscordRegistration :one
SELECT * FROM discord_registrations WHERE discord_user_id = $1 AND guild_id = $2 LIMIT 1;

-- name: LinkDiscordRegistrationToUser :exec
UPDATE discord_registrations SET user_id = $1, updated_at = NOW() WHERE discord_user_id = $2;

-- name: ListDiscordRegistrationsByUser :many
SELECT * FROM discord_registrations WHERE user_id = $1 ORDER BY created_at;

-- name: DeleteDiscordRegistration :exec
DELETE FROM discord_registrations WHERE discord_user_id = $1 AND guild_id = $2;

-- name: CountDiscordRegistrationsByGuild :one
SELECT COUNT(*) FROM discord_registrations WHERE guild_id = $1;
