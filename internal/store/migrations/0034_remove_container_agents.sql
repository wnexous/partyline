-- Remove the container agents system (AD-039): runners, owner secrets and the container AIs.
-- The owner decided on 2026-11-04 to remove the whole feature and build Partyline's own harness
-- later, so its rows are dropped and its tokens are revoked. The automatic pre-migration backup
-- (OPS-07) keeps a copy of everything this migration deletes.

-- The container of an AI (CA-04..CA-06), its named credentials (CA-03) and the servers that ran
-- them (CA-01, CA-02). container_agents names runners with ON DELETE RESTRICT, so it goes first.
DROP TABLE container_agents;
DROP TABLE owner_secrets;
DROP TABLE runners;

-- A runner token is useless: the runner routes and the kind that accepted it are gone. Its
-- device requests go too, so no pending or approved request can deliver a token of a kind the
-- server no longer creates (and the sealed token does not outlive it). device_requests.token_id
-- is ON DELETE SET NULL, so the delete of the token itself is never blocked.
DELETE FROM device_requests WHERE kind = 'runner';
DELETE FROM api_tokens WHERE kind = 'runner';

-- The CHECKs stay as they are: api_tokens.kind still accepts 'runner' and device_requests.kind
-- still has the column with 'agent' and 'runner', because rebuilding either table to narrow a
-- CHECK is not worth the risk here. Nothing writes 'runner' any more and the migration leaves no
-- such row, so the values are unreachable until a later cleanup drops them.
