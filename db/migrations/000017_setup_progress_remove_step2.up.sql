-- The wizard's old step 2 (a "Done" holding step for a two-tab bot-install
-- flow) is gone now that the flow is single-tab and step-advances only once
-- the install round-trip actually completes. Anyone caught sitting on that
-- old step 2 is in an ambiguous state — we don't know whether they actually
-- finished installing the bot in the other tab — so send them back to step 1
-- to redo the question cleanly. Everyone from the old step 3 onward shifts
-- down by one to close the gap, including the terminal "done" sentinel
-- (6 -> 5).
UPDATE users SET setup_progress = 1 WHERE setup_progress = 2;
UPDATE users SET setup_progress = setup_progress - 1 WHERE setup_progress >= 3;
