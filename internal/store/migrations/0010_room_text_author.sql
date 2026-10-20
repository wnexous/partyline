-- Room text author (routing-context fix iteration 2).
-- updated_by: the user who created the room or last changed its name or description (INS-06);
-- '' when unknown (rooms created or renamed before this migration), which no agent trusts.
-- Numbered 0010 because 0009 is device_requests on main (harness-onboarding).
ALTER TABLE rooms ADD COLUMN updated_by TEXT NOT NULL DEFAULT '';
