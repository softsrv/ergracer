-- Originally ON DELETE SET NULL, so a registration created via a Discord
-- slash command (independent of any site account) would survive the linked
-- account being deleted. Account deletion is meant to erase everything tied
-- to that user, so switch to CASCADE — rows with a NULL user_id (never
-- linked to a site account) are untouched either way.
ALTER TABLE discord_registrations
    DROP CONSTRAINT discord_registrations_user_id_fkey,
    ADD CONSTRAINT discord_registrations_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
