-- A photo for groups (messenger-polish, GAV-05, AD-012). rooms.avatar_sha256 is the blob of the 256x256 PNG a
-- manager gave the group, the empty string for none: the same column as the avatar of an intelligence or of an
-- organization, counted by the retention sweep as a reference to the blob. A DM never has one: it shows the
-- other party. Additive: ADD COLUMN only, so no table that other tables reference is rebuilt.

ALTER TABLE rooms ADD COLUMN avatar_sha256 TEXT NOT NULL DEFAULT '';
CREATE INDEX rooms_avatar ON rooms (avatar_sha256) WHERE avatar_sha256 <> '';
