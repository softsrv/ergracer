-- The setup wizard gained a new step 2 (a "Done" holding step for while the
-- user is off in the Discord tab installing the bot), shifting every step
-- from the old step 2 onward up by one — including the terminal "done"
-- sentinel (5 -> 6). Users still on the old step 1 (the yes/no question) are
-- unaffected, since that step didn't move.
UPDATE users SET setup_progress = setup_progress + 1 WHERE setup_progress >= 2;
