-- A realistic v0.3 (schema 0014) data set for the intelligences upgrade tests. Synthetic
-- values only. Times are unix milliseconds around 1790000000000.
INSERT INTO users (id, username, display_name, password_hash, role, locale, disabled_at, created_at) VALUES
    ('u-alice', 'alice', 'Alice A.', 'x', 'admin', 'pt-BR', NULL, 1790000000001),
    ('u-bob', 'Bob.Smith', '', 'x', 'member', '', NULL, 1790000000002),
    ('u-pl', 'partyline', '', 'x', 'member', '', 1790000000900, 1790000000003);

INSERT INTO sessions (id, token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, ip) VALUES
    ('s-alice', 'session-hash-1', 'u-alice', 1790000000010, 1799000000000, 1790000000010, 'Firefox', '192.0.2.10');
INSERT INTO api_tokens (id, user_id, kind, name, prefix, token_hash, created_at, last_used_at, expires_at, revoked_at) VALUES
    ('t-alice', 'u-alice', 'agent', 'devbox', 'pl_agent_ab', 'token-hash-1', 1790000000011, 1790000000500, NULL, NULL);
INSERT INTO invites (id, token_hash, role, created_by, created_at, expires_at, used_by, used_at, revoked_at) VALUES
    ('inv-1', 'invite-hash-1', 'member', 'u-alice', 1790000000001, 1790000100000, 'u-bob', 1790000000002, NULL);
INSERT INTO device_requests (id, code_hash, poll_secret_hash, machine, created_ip, ip_key, status, user_id, token_id, token_once_enc, created_at, expires_at, last_poll_at) VALUES
    ('dr-1', 'code-hash-1', 'poll-hash-1', 'devbox', '192.0.2.10', '192.0.2.10', 'approved', 'u-alice', 't-alice', NULL, 1790000000011, 1790000000611, 1790000000012);
INSERT INTO device_code_attempts (id, user_id, attempted_at) VALUES (1, 'u-alice', 1790000000011);
INSERT INTO audit_log (id, actor_id, action, target_kind, target_id, detail_json, ip, created_at) VALUES
    ('au-1', 'u-alice', 'user.create', 'user', 'u-bob', '{}', '192.0.2.10', 1790000000002);

-- Projects: Proj has two temporaries and a never-connected one; Empty has no agents; atlas
-- is the project of the fixed agent atlas (0011 gave existing agents the project of their name).
INSERT INTO projects (id, owner_id, slug, name, created_at) VALUES
    ('p-proj', 'u-alice', 'proj', 'Proj', 1790000000004),
    ('p-empty', 'u-alice', 'empty', 'Empty', 1790000000004),
    ('p-atlas', 'u-alice', 'atlas', 'Atlas', 1790000000004);

INSERT INTO agents (id, owner_id, name, description, created_at, last_seen_at, project_id, harness, machine, kind,
                    display_name, auto_name, session_no, archived_at, connected_at, session_key) VALUES
    ('a-atlas', 'u-alice', 'atlas', 'fixed agent', 1790000000005, 1790000000500, 'p-atlas', 'claude', 'devbox', 'fixed', '', '', 0, NULL, 1790000000500, ''),
    ('a-pc', 'u-alice', 'proj-claude', '', 1790000000006, 1790000000100, 'p-proj', 'claude', 'devbox', 'temporary', '', 'Proj · Claude', 1, NULL, 1790000000100, ''),
    ('a-pc2', 'u-alice', 'proj-claude-2', '', 1790000000007, 1790000000200, 'p-proj', 'claude', 'notebook', 'temporary', '', 'Proj · Claude #2', 2, NULL, 1790000000200, ''),
    ('a-pc0', 'u-alice', 'proj-codex', '', 1790000000008, NULL, 'p-proj', 'codex', 'devbox', 'temporary', '', 'Proj · Codex', 1, NULL, NULL, 'session-key-1'),
    ('a-agent', 'u-alice', 'agent', '', 1790000000009, 1790000000300, NULL, 'agent', 'devbox', 'temporary', 'Scratch', '', 0, 1790000000400, 1790000000300, ''),
    ('a-bob-atlas', 'u-bob', 'atlas', '', 1790000000010, NULL, NULL, 'agent', '', 'fixed', '', '', 0, NULL, NULL, '');

INSERT INTO rooms (id, owner_id, name, description, bridge_chats, retention_days, next_seq, agent_streak, agents_paused,
                   created_at, updated_at, instructions, broadcast, instructions_by, updated_by) VALUES
    ('r-ops', 'u-alice', 'ops', 'Operations', 1, 90, 8, 1, 0, 1790000000020, 1790000000020, 'Be brief.', 1, 'u-alice', 'u-alice'),
    ('r-inbox', 'u-alice', 'inbox', '', 0, 30, 4, 0, 0, 1790000000021, 1790000000021, '', 0, '', 'u-alice'),
    ('r-dev', 'u-alice', 'dev', '', 0, 90, 3, 0, 0, 1790000000022, 1790000000022, '', 1, '', 'u-alice'),
    ('r-bob', 'u-bob', 'bob-room', '', 0, 90, 2, 0, 1, 1790000000023, 1790000000023, '', 1, '', 'u-bob');

INSERT INTO connectors (id, owner_id, kind, name, config_json, secrets_enc, enabled, status, last_error, created_at, updated_at,
                        auto_route_scope, auto_route_room_id) VALUES
    ('k-tg', 'u-alice', 'telegram', 'Support bot', '{}', NULL, 1, 'connected', '', 1790000000030, 1790000000030, 'dms', 'r-inbox'),
    ('k-wa', 'u-alice', 'whatsapp', 'Phone', '{}', NULL, 1, 'connected', '', 1790000000031, 1790000000031, 'off', NULL);

INSERT INTO chats (id, connector_id, external_id, title, kind, last_seen_at, created_at) VALUES
    ('c-tgdm', 'k-tg', '100', 'Maria', 'dm', 1790000000062, 1790000000040),
    ('c-tggrp', 'k-tg', '-1001', 'Family', 'group', 1790000000052, 1790000000041),
    ('c-wagrp', 'k-wa', '120363000000000001@g.us', 'Team', 'group', 1790000000054, 1790000000042);

INSERT INTO identities (id, user_id, connector_id, connector_kind, external_id, alt_external_id, display, created_at) VALUES
    ('i-bob', 'u-bob', 'k-tg', 'telegram', '101', '', 'Bob TG', 1790000000043);
INSERT INTO pairing_codes (code_hash, user_id, connector_id, expires_at) VALUES ('pair-hash-1', 'u-bob', 'k-tg', 1799000000000);
INSERT INTO auto_route_skips (connector_id, chat_id, until) VALUES ('k-tg', 'c-tggrp', 1799000000000);
INSERT INTO webhook_replays (connector_id, sig_hash, expires_at) VALUES ('k-tg', 'sig-hash-1', 1799000000000);

-- Members of every kind. Proj is a member of ops with its own settings and of dev, where its
-- latest session is also a direct member; Empty (no agents) is a member of dev.
INSERT INTO room_members (id, room_id, kind, member_id, outbound, added_by, created_at, display_name, sign_outbound, delivery, keywords_json) VALUES
    ('rm-ops-atlas', 'r-ops', 'agent', 'a-atlas', 1, 'u-alice', 1790000000024, 'Atlas Ops', 0, 'mentions', '["deploy"]'),
    ('rm-ops-proj', 'r-ops', 'project', 'p-proj', 0, 'u-alice', 1790000000025, 'Proj Bot', 0, 'trusted', '["build","ci"]'),
    ('rm-ops-bob', 'r-ops', 'user', 'u-bob', 1, 'u-alice', 1790000000026, '', 1, 'all', '[]'),
    ('rm-ops-tggrp', 'r-ops', 'chat', 'c-tggrp', 1, 'u-alice', 1790000000027, '', 1, 'all', '[]'),
    ('rm-ops-wagrp', 'r-ops', 'chat', 'c-wagrp', 0, 'u-alice', 1790000000028, '', 1, 'all', '[]'),
    ('rm-inbox-atlas', 'r-inbox', 'agent', 'a-atlas', 1, 'u-alice', 1790000000029, '', 1, 'all', '[]'),
    ('rm-inbox-tgdm', 'r-inbox', 'chat', 'c-tgdm', 1, '', 1790000000040, '', 1, 'all', '[]'),
    ('rm-dev-pc2', 'r-dev', 'agent', 'a-pc2', 1, 'u-alice', 1790000000032, 'Mine', 1, 'all', '[]'),
    ('rm-dev-proj', 'r-dev', 'project', 'p-proj', 1, 'u-alice', 1790000000033, 'Proj Dev', 0, 'mentions', '["x"]'),
    ('rm-dev-empty', 'r-dev', 'project', 'p-empty', 1, 'u-alice', 1790000000034, '', 1, 'all', '[]'),
    ('rm-bob-agent', 'r-bob', 'agent', 'a-bob-atlas', 1, 'u-bob', 1790000000035, '', 1, 'all', '[]');

INSERT INTO grants (resource_kind, resource_id, subject, level, created_by, created_at) VALUES
    ('room', 'r-ops', 'u-bob', 'post', 'u-alice', 1790000000036),
    ('room', 'r-inbox', 'u-bob', 'view', 'u-alice', 1790000000036),
    ('agent', 'a-atlas', '*', 'use', 'u-alice', 1790000000037),
    ('connector', 'k-tg', 'u-bob', 'use', 'u-alice', 1790000000038),
    ('project', 'p-proj', 'u-bob', 'use', 'u-alice', 1790000000039);

INSERT INTO messages (id, room_id, seq, author_kind, author_id, author_user_id, sender_id, sender_alt_id, sender_name, kind, text,
                      reply_to, recipients_json, external_id, created_at) VALUES
    ('m-ops1', 'r-ops', 1, 'user', 'u-alice', '', '', '', '', 'text', 'hello', '', '[]', NULL, 1790000000050),
    ('m-ops2', 'r-ops', 2, 'agent', 'a-atlas', '', '', '', '', 'text', 'hi', '', '[]', NULL, 1790000000051),
    ('m-ops3', 'r-ops', 3, 'chat', 'c-tggrp', 'u-bob', '101', '', 'Bobby', 'text', 'from bob', '', '[]', 'tg-1', 1790000000052),
    ('m-ops4', 'r-ops', 4, 'chat', 'c-wagrp', '', '100000000000002@lid', '5511999990002@s.whatsapp.net', 'Zé', 'text', 'photo', '', '[]', 'wa-1', 1790000000053),
    ('m-ops5', 'r-ops', 5, 'chat', 'c-wagrp', '', '', '', '', 'text', 'no sender', '', '[]', 'wa-2', 1790000000054),
    ('m-ops6', 'r-ops', 6, 'system', '', '', '', '', '', 'notice', 'Agents paused', '', '[]', NULL, 1790000000055),
    ('m-ops7', 'r-ops', 7, 'agent', 'a-pc2', '', '', '', '', 'text', 'build ok', 'm-ops1', '["c-tggrp"]', NULL, 1790000000056),
    ('m-in1', 'r-inbox', 1, 'chat', 'c-tgdm', '', '100', '', 'Maria', 'text', 'quero um orçamento', '', '[]', 'tg-2', 1790000000060),
    ('m-in2', 'r-inbox', 2, 'agent', 'a-atlas', '', '', '', '', 'text', 'claro', 'm-in1', '["c-tgdm"]', NULL, 1790000000061),
    ('m-in3', 'r-inbox', 3, 'chat', 'c-tgdm', '', '100', '', 'Maria Silva', 'text', 'obrigada', '', '[]', 'tg-3', 1790000000062),
    ('m-dev1', 'r-dev', 1, 'user', 'u-alice', '', '', '', '', 'text', 'dev 1', '', '[]', NULL, 1790000000063),
    ('m-dev2', 'r-dev', 2, 'agent', 'a-pc2', '', '', '', '', 'text', 'dev 2', '', '[]', NULL, 1790000000064),
    ('m-bob1', 'r-bob', 1, 'user', 'u-bob', '', '', '', '', 'text', 'bob only', '', '[]', NULL, 1790000000065);

INSERT INTO attachments (id, message_id, sha256, mime, name, size, status, created_at) VALUES
    ('f-1', 'm-ops4', 'aaaa000000000000000000000000000000000000000000000000000000000001', 'image/jpeg', 'photo.jpg', 2048, 'stored', 1790000000053),
    ('f-2', 'm-in3', 'aaaa000000000000000000000000000000000000000000000000000000000002', 'application/pdf', 'quote.pdf', 4096, 'too_large', 1790000000062);

INSERT INTO outbox (id, message_id, chat_id, status, attempts, next_attempt_at, last_error, external_id, updated_at) VALUES
    ('o-1', 'm-in2', 'c-tgdm', 'sent', 1, 1790000000061, '', 'tg-out-1', 1790000000061),
    ('o-2', 'm-ops1', 'c-tggrp', 'pending', 0, 1790000000050, '', '', 1790000000050),
    ('o-3', 'm-ops7', 'c-tggrp', 'failed', 5, 1790000000070, 'timeout', '', 1790000000070);

-- Cursors: atlas has read everything; Proj is ahead of its latest session in ops and behind
-- it in dev.
INSERT INTO agent_cursors (agent_id, room_id, seq) VALUES
    ('a-atlas', 'r-ops', 7), ('a-atlas', 'r-inbox', 3), ('a-pc2', 'r-ops', 3), ('a-pc2', 'r-dev', 2), ('a-bob-atlas', 'r-bob', 1);
INSERT INTO project_cursors (project_id, room_id, seq) VALUES ('p-proj', 'r-ops', 5), ('p-proj', 'r-dev', 1);
