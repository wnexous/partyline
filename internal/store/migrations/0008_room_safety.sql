-- Room safety (routing-context fix iteration 1).
-- broadcast: agent and user posts without explicit recipients reach every chat with outbound
-- on (DLV-03). Off, they reach no chat (ROUTE-06). Rooms that are a connector's catch-all room
-- are kept off, without chat bridging (ROUTE-07).
ALTER TABLE rooms ADD COLUMN broadcast BOOLEAN NOT NULL DEFAULT 1;
UPDATE rooms SET broadcast = 0, bridge_chats = 0
WHERE id IN (SELECT auto_route_room_id FROM connectors WHERE auto_route_room_id IS NOT NULL);

-- instructions_by: the user who last saved the room's instructions (INS-04); '' when unknown
-- (instructions saved before this migration), which no agent trusts.
ALTER TABLE rooms ADD COLUMN instructions_by TEXT NOT NULL DEFAULT '';
