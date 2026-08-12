-- name: CreateUser :one
INSERT INTO users (id, email, email_verified, created_at, updated_at)
VALUES ($1, $2, true, NOW(), NOW())
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1 LIMIT 1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 LIMIT 1;

-- name: SetEmailVerified :exec
UPDATE users SET email_verified = true, updated_at = NOW() WHERE id = $1;

-- name: DeleteUserByID :exec
DELETE FROM users WHERE id = $1;

-- name: SetSetupProgress :exec
UPDATE users SET setup_progress = $2, updated_at = NOW() WHERE id = $1;
