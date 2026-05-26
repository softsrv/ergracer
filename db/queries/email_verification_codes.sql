-- name: InsertEmailVerificationCode :one
INSERT INTO email_verification_codes (id, user_id, code, expires_at, created_at)
VALUES ($1, $2, $3, $4, NOW())
RETURNING *;

-- name: GetLatestUnusedVerificationCode :one
SELECT * FROM email_verification_codes
WHERE user_id = $1 AND used_at IS NULL AND expires_at > NOW()
ORDER BY created_at DESC
LIMIT 1;

-- name: MarkVerificationCodeUsed :exec
UPDATE email_verification_codes SET used_at = NOW() WHERE id = $1;

-- name: CountRecentVerificationCodesByUserID :one
SELECT COUNT(*) FROM email_verification_codes
WHERE user_id = $1 AND created_at > NOW() - INTERVAL '1 hour';

-- name: DeleteStaleVerificationCodes :execrows
DELETE FROM email_verification_codes
WHERE (used_at IS NOT NULL AND used_at < NOW() - INTERVAL '7 days')
   OR (expires_at < NOW() - INTERVAL '1 day');
