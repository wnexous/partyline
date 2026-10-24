-- A send that is attempted once (v0.6.2, CID-32). A file an AI sends through a connection is held in
-- memory for the one attempt and never stored, because no copy of what a person is sent outlives its
-- delivery (AD-015): the worker cannot retry such a send, and a process that dies while it is sending
-- must fail it at the next start instead of sending its text again without the file. Additive
-- (AD-012): every existing send keeps the default and is retried as before.
ALTER TABLE platform_sends ADD COLUMN once BOOLEAN NOT NULL DEFAULT 0 CHECK (once IN (0, 1));
