-- Quoted context on platform messages (quoted-context feature, QC-01..QC-05). Additive (AD-012):
-- every existing item and send keeps the defaults, which say "no quote".
--
-- A platform message that quotes another carries, on its queued item, what the AI needs to know
-- which message was quoted, so that a message delivered later (the AI was offline) still has it:
--   reply_to         the Partyline id of the quoted message when Partyline knows it: the id of the
--                    AI's send (platform_sends.id, what the send tool returned) or of an earlier item
--                    of the same chat (QC-01);
--   quoted           a short preview of the quoted message, taken from the platform event (QC-02).
--                    It is platform text: it is cleared on ack with the item's text (CID-09, QC-05);
--   quoted_from_self the quoted message is one the account itself wrote (QC-03).
ALTER TABLE platform_items ADD COLUMN reply_to TEXT NOT NULL DEFAULT '';
ALTER TABLE platform_items ADD COLUMN quoted TEXT NOT NULL DEFAULT '';
ALTER TABLE platform_items ADD COLUMN quoted_from_self BOOLEAN NOT NULL DEFAULT 0 CHECK (quoted_from_self IN (0, 1));

-- The platform ids of the messages a send created after its first one (the file after a caption,
-- the parts of a long text): platform_sends.external_id keeps only the first, and a quote of any of
-- them names the send (QC-01). A row goes with its send, after SendRetention.
CREATE TABLE platform_send_parts (
    send_id     TEXT NOT NULL REFERENCES platform_sends (id) ON DELETE CASCADE,
    chat_id     TEXT NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    external_id TEXT NOT NULL,
    PRIMARY KEY (send_id, external_id)
);
CREATE INDEX platform_send_parts_chat ON platform_send_parts (chat_id, external_id);
