-- The session key of the tools-only session that holds a temporary agent (agent-identity fix
-- iteration 1, ID-01). Tools-only sessions never come online, so presence cannot tell two of
-- them apart: a session that registers without a name and with X-Partyline-Session gets its
-- own agent back by this key, and other sessions skip an agent held by another key until it
-- is archived. '' = not held (live sessions clear it on connect).
ALTER TABLE agents ADD COLUMN session_key TEXT NOT NULL DEFAULT '';
