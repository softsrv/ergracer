-- name: UpsertOAuthToken :one
INSERT INTO oauth_tokens (id, oauth_identity_id, access_token_enc, refresh_token_enc, scope, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
ON CONFLICT (oauth_identity_id) DO UPDATE
    SET access_token_enc  = EXCLUDED.access_token_enc,
        refresh_token_enc = EXCLUDED.refresh_token_enc,
        scope             = EXCLUDED.scope,
        expires_at        = EXCLUDED.expires_at,
        updated_at        = NOW()
RETURNING *;

-- name: GetOAuthTokenByIdentity :one
SELECT * FROM oauth_tokens WHERE oauth_identity_id = $1 LIMIT 1;
