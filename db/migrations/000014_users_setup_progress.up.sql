ALTER TABLE users ADD COLUMN IF NOT EXISTS setup_progress INTEGER NOT NULL DEFAULT 1;

-- One-time backfill: existing users who've already functionally completed
-- the dashboard setup wizard (registered in a Discord server AND linked
-- Concept2) shouldn't be shown it from step 1 again once this ships. Joins
-- through oauth_identities.provider_user_id rather than
-- discord_registrations.user_id directly, since some registrations predate
-- the account being linked and have a NULL user_id there.
UPDATE users u
SET setup_progress = 5
WHERE EXISTS (
    SELECT 1
    FROM oauth_identities oi
    JOIN discord_registrations dr ON dr.discord_user_id = oi.provider_user_id
    WHERE oi.user_id = u.id AND oi.provider = 'discord'
)
AND EXISTS (
    SELECT 1 FROM oauth_identities oi2 WHERE oi2.user_id = u.id AND oi2.provider = 'concept2'
);
