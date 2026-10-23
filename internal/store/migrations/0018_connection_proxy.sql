-- Connection proxy upgrade (connection-identity feature, AD-015, AD-012). Platforms leave the rooms:
-- what arrives on a connection goes to its operator's queue (0017) and a room holds users and AIs
-- only. Rooms keep their history, intelligences and settings; people, chat_people and the DM rooms
-- with people stay as read-only history. No table that other tables reference is rebuilt: columns
-- go with ALTER TABLE ... DROP COLUMN, which SQLite refuses while a view, trigger, index or
-- constraint names the column. None does: message_views names no room or connector column, the
-- triggers use OLD.id only, and the CHECK on auto_route_scope belongs to the column being dropped.

-- CID-21: every chat leaves its room. The room keeps its messages (including those the chat
-- wrote), its other members and its settings.
DELETE FROM room_members WHERE kind = 'chat';

-- Nothing routes chats to rooms or copies AI DMs to a user's app any more. The two columns stay as
-- documented dead foreign keys (AD-012): auto_route_room_id (a connection's catch-all room) and
-- users.notify_chat_id (the chat that received copies of AI DMs).
UPDATE connectors SET auto_route_room_id = NULL;
UPDATE users SET notify_chat_id = NULL;

-- operator_iid is left alone: NULL now means "no operator" (CID-02). An owner-operated connection
-- idles until its owner chooses an AI. deliver and groups_mentions_only already hold the CID-27
-- defaults from 0017, whatever the old catch-all scope was.

-- Delivery to chats and the catch-all skip list: nothing references either table, so they drop
-- with their indexes.
DROP TABLE outbox;
DROP TABLE auto_route_skips;

-- The catch-all scope, and the room switches that sent a room's posts to its chats.
ALTER TABLE connectors DROP COLUMN auto_route_scope;
ALTER TABLE rooms DROP COLUMN bridge_chats;
ALTER TABLE rooms DROP COLUMN broadcast;

-- Kept as documented dead schema (AD-012): room_members.outbound and sign_outbound (the API no
-- longer reads or writes them), connectors.auto_route_room_id and users.notify_chat_id (emptied
-- above).
