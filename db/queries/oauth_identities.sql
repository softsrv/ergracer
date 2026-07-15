-- name: CreateOAuthIdentity :one
INSERT INTO oauth_identities (id, user_id, provider, provider_user_id, provider_username, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
RETURNING *;

-- name: GetOAuthIdentityByProviderUserID :one
SELECT * FROM oauth_identities WHERE provider = $1 AND provider_user_id = $2 LIMIT 1;

-- name: GetOAuthIdentityByUserAndProvider :one
SELECT * FROM oauth_identities WHERE user_id = $1 AND provider = $2 LIMIT 1;

-- name: ListOAuthIdentitiesByUser :many
SELECT * FROM oauth_identities WHERE user_id = $1 ORDER BY created_at;

-- name: DeleteOAuthIdentity :exec
DELETE FROM oauth_identities WHERE id = $1;
