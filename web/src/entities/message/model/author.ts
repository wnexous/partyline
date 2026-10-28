import { InfoIcon, type LucideIcon, MessageCircleIcon, UserIcon } from "lucide-react";
import type { AuthorKind, IntelligenceRef, Message } from "@/shared/api/types.gen";
import type { MessageKey } from "@/shared/i18n";

/*
 * AIs and users read alike (AD-024): neither is named by its kind, for the eye or for assistive
 * technology, and one with no profile to draw a face from gets the same plain tile. Only a chat
 * sender and the system are named, because that says where the text came from.
 */
export const UNRESOLVED_TILE = "bg-secondary text-foreground ring-1 ring-border ring-inset";

export const AUTHOR_KIND: Record<
  AuthorKind,
  { icon: LucideIcon; label: MessageKey | null; tile: string }
> = {
  agent: { icon: UserIcon, label: null, tile: UNRESOLVED_TILE },
  chat: {
    icon: MessageCircleIcon,
    label: "message.kind.chat",
    tile: "bg-tape text-tape-foreground",
  },
  user: { icon: UserIcon, label: null, tile: UNRESOLVED_TILE },
  system: { icon: InfoIcon, label: "message.kind.system", tile: "bg-muted text-muted-foreground" },
};

export type Trust = "trusted" | "verified" | "untrusted";

// SPEC_DEVIATION: dto.Message has no `trusted` flag (trust is recipient-relative, computed per
// agent). Reason: the web viewer derives it from author_user_id: empty → untrusted for every
// agent; the viewer's id → trusted by the viewer's agents; another user → verified (only theirs).
/**
 * Trust of a chat message from the viewer's side (AD-006): the server resolves the author to a
 * user only for verified chat identities. Verified as the viewer → the viewer's agents trust it.
 */
export function chatTrust(message: Message, meId: string): Trust {
  if (!message.author_user_id) return "untrusted";
  return message.author_user_id === meId ? "trusted" : "verified";
}

/**
 * The intelligence a row shows as its author (PROF-03, PROF-09): a user or an AI always; a chat
 * sender only once it resolved to a person or to the viewer's own account (GRP-03, PEOP-02).
 * Unattributed senders keep the generic chat tile. The name is the one the timeline shows, so a
 * name an owner set for an AI in the room (GRP-04) wins over its profile name.
 */
export function authorProfile(message: Message): IntelligenceRef | null {
  const author = message.author;
  if (!author?.id) return null;
  const known =
    message.author_kind === "user" ||
    message.author_kind === "agent" ||
    (message.author_kind === "chat" &&
      (author.role === "person" || author.id === message.author_user_id));
  if (!known) return null;
  return { ...author, display_name: message.author_name || author.display_name };
}

export function authorDisplayName(message: Message): string {
  if (message.author_kind === "chat") {
    return message.sender_name || message.sender_id || message.author_name;
  }
  return message.author_name;
}
