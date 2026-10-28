/*
 * What a viewer may do with one message (EDT-02, EDT-03, DLT-02, DLT-03): edit their own text and
 * delete their own or, in a group they manage, anyone's but a system notice. The server decides;
 * these rules only keep the menu from offering what it would refuse. Read by the message menu and
 * by the composer's edit.
 */

import type { Message } from "@/shared/api/types.gen";

/** The viewer wrote the message here: a person's own message, not a chat's or an AI's, whoever they host. */
export const isOwnMessage = (m: Message, meId: string) =>
  m.author_kind === "user" && m.author_id === meId;

/** Edit: the viewer's own text message that is still there (EDT-02, EDT-03). */
export const canEditMessage = (m: Message, meId: string) =>
  isOwnMessage(m, meId) && m.kind === "text" && m.deleted_at === undefined;

/**
 * Delete: the viewer's own message and, when `canModerate` (they manage the group; nobody manages a DM), any
 * other, but never a system notice and never one that is deleted already (DLT-02, DLT-03).
 */
export const canDeleteMessage = (m: Message, meId: string, canModerate: boolean) =>
  m.deleted_at === undefined &&
  m.author_kind !== "system" &&
  (isOwnMessage(m, meId) || canModerate);
