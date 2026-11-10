-- Read by any session (SN-11): what a session of an AI has read in a room is the run of messages after the
-- point its state row was created at and up to its read position, read_from < seq <= read_seq. A row created
-- at the session's join point (SN-01) starts caught up without having read what came before, so read_from is
-- that join point; a row created by a reading (an acknowledgement or a read_unread in a room the session had
-- no row in) has read everything up to it, and keeps 0. The backlog that another session of the AI is given
-- on attach leaves out what any session read. A row that exists already is given read_from = read_seq: what
-- it read before this migration is not known, so nothing of it counts (no message is ever left out because
-- of a guess).
ALTER TABLE session_room_state ADD COLUMN read_from INTEGER NOT NULL DEFAULT 0;
UPDATE session_room_state SET read_from = read_seq;
