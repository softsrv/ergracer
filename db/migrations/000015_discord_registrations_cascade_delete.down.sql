ALTER TABLE discord_registrations
    DROP CONSTRAINT discord_registrations_user_id_fkey,
    ADD CONSTRAINT discord_registrations_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
