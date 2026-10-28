import { IntelligenceAvatar } from "@/entities/intelligence";
import type { IntelligenceRef, RoomMember } from "@/lib/api/types.gen";
import { useI18n } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";

/*
 * @mention suggestions (MEN-01..MEN-06). The candidates are the conversation's participants that
 * have a handle - the AIs and users of the room and the people seen in its chats - minus the
 * viewer. An AI with live labelled sessions is offered once for itself (the principal) and once
 * per labelled session (SESS-33), so `@handle:label` reaches a parallel session instead of the
 * AI. The text after the `@` filters them by display name, handle or label, case and accents
 * ignored; the list shows at most eight and never sends on its own: picking one replaces the
 * `@word` with `@handle:label ` (or `@handle ` for the principal) and a trailing space. The list
 * is a listbox driven by the textarea's caret (combobox roles, `aria-activedescendant` names the
 * active option), which also makes it tappable on a phone.
 */

/** MEN-01: the list stops here. */
export const MaxMentionSuggestions = 8;

/** An `@word` under the caret. */
export interface MentionMatch {
  /** Index of the `@`. */
  start: number;
  /** Index just past the word, where the replacement ends. */
  end: number;
  /** What was typed between the `@` and the caret. */
  query: string;
}

/** A character that keeps an `@` inside the same word (letters, digits, `_` and handle `-`). */
const TOKEN_CHAR = /[\p{L}\p{N}_-]/u;
/** A character that keeps an `@` from starting a word (T12's rule for mentions). */
const WORD_CHAR = /[\p{L}\p{N}_]/u;

/**
 * The `@word` the caret is in (MEN-01, MEN-04), or null when the caret is outside one. The `@`
 * has to start a word, so an address such as `joe@atlas.example` opens nothing (T12).
 */
export function mentionAt(text: string, caret: number): MentionMatch | null {
  if (caret < 0 || caret > text.length) return null;
  // Walk back over the word's characters to the `@`; anything else is not a mention.
  let start = caret;
  while (start > 0 && TOKEN_CHAR.test(text.charAt(start - 1))) start -= 1;
  if (text.charAt(start - 1) !== "@") return null;
  start -= 1;
  if (start > 0 && WORD_CHAR.test(text.charAt(start - 1))) return null;
  // The word runs to the first character that is not part of it (whitespace or punctuation).
  let end = caret;
  while (end < text.length && TOKEN_CHAR.test(text.charAt(end))) end += 1;
  return { start, end, query: text.slice(start + 1, caret) };
}

/** One row of the list: an intelligence, and the session it is addressed at ("" = the AI itself). */
export interface MentionCandidate {
  intelligence: IntelligenceRef;
  /** The session's label; "" offers the principal (the AI, a user or a person). */
  label: string;
}

/** The key of a candidate: the principal is `id:` and a session `id:label`, so no label collides. */
const candidateKey = (id: string, label: string) => `${id}:${label ? `:${label}` : ""}`;

/** Accent- and case-insensitive form for matching (MEN-02). */
const fold = (value: string) =>
  value
    .normalize("NFD")
    .replace(/\p{Diacritic}/gu, "")
    .toLowerCase();

/**
 * The participants a mention can reach: the intelligence of agent and user members and the
 * people seen in chat members, with a handle and without the viewer (MEN-01). An agent member
 * adds one candidate for the AI itself plus one for each live labelled session it carries
 * (SESS-33); a human, a person and an AI with no sessions add their plain handle only.
 */
export function mentionCandidates(members: RoomMember[], viewerId?: string): MentionCandidate[] {
  const seen = new Set<string>();
  const candidates: MentionCandidate[] = [];
  const add = (intelligence: IntelligenceRef, label = "") => {
    if (!intelligence.handle || intelligence.id === viewerId) return;
    const key = candidateKey(intelligence.id, label);
    if (seen.has(key)) return;
    seen.add(key);
    candidates.push({ intelligence, label });
  };
  for (const member of members) {
    if (member.kind === "agent" || member.kind === "user") {
      if (member.intelligence) {
        add(member.intelligence);
        if (member.kind === "agent") {
          for (const session of member.sessions ?? []) {
            if (session.label) add(member.intelligence, session.label);
          }
        }
      }
    }
    for (const person of member.people ?? []) add(person);
  }
  return candidates;
}

/**
 * Filters the candidates by what was typed after the `@` (MEN-02) and keeps the first eight
 * (MEN-01). The label of a labelled session matches too, so `@claude` finds `@atlas:claude-2`.
 * An empty query offers everyone.
 */
export function matchMentions(candidates: MentionCandidate[], query: string): MentionCandidate[] {
  const needle = fold(query.trim());
  // `handle:label` as typed: the part before the colon is the handle, the part after the label.
  const colon = needle.indexOf(":");
  const matched = !needle
    ? candidates
    : colon >= 0
      ? candidates.filter(
          ({ intelligence, label }) =>
            intelligence.handle.toLowerCase().includes(needle.slice(0, colon)) &&
            label.toLowerCase().includes(needle.slice(colon + 1)),
        )
      : candidates.filter(
          ({ intelligence, label }) =>
            fold(intelligence.display_name).includes(needle) ||
            intelligence.handle.toLowerCase().includes(needle) ||
            label.toLowerCase().includes(needle),
        );
  return matched.slice(0, MaxMentionSuggestions);
}

/** The DOM id of an option, so the combobox can name the active one. */
export const mentionOptionId = (listId: string, candidate: MentionCandidate) =>
  `${listId}-${candidateKey(candidate.intelligence.id, candidate.label)}`;

export interface MentionSuggestionsProps {
  id: string;
  candidates: MentionCandidate[];
  /** Index of the highlighted option. */
  activeIndex: number;
  onPick: (candidate: MentionCandidate) => void;
}

/**
 * The listbox above the composer. Options are chosen with the keyboard from the textarea
 * (`aria-activedescendant`) or tapped; they are not focusable themselves.
 */
export function MentionSuggestions({
  id,
  candidates,
  activeIndex,
  onPick,
}: MentionSuggestionsProps) {
  const { t } = useI18n();
  return (
    <div
      id={id}
      role="listbox"
      aria-label={t("composer.mentions")}
      className="absolute inset-x-0 bottom-full z-20 mb-1 max-h-64 overflow-y-auto rounded-lg border bg-popover p-1 shadow-md"
    >
      {candidates.map((candidate, index) => {
        const active = index === activeIndex;
        return (
          <div
            key={candidateKey(candidate.intelligence.id, candidate.label)}
            id={mentionOptionId(id, candidate)}
            role="option"
            aria-selected={active}
            tabIndex={-1}
            className={cn(
              "flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-2.5 py-1.5",
              active ? "bg-accent text-accent-foreground" : "hover:bg-accent/60",
            )}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => onPick(candidate)}
            onKeyDown={(e) => {
              // Keyboard picks happen in the textarea; this also keeps the option usable when a
              // screen reader moves focus to it.
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onPick(candidate);
              }
            }}
          >
            <IntelligenceAvatar intelligence={candidate.intelligence} size="sm" decorative />
            <span className="min-w-0 flex-1 truncate text-sm font-medium">
              {candidate.intelligence.display_name.trim() || candidate.intelligence.handle}
            </span>
            {/* The label is part of the address the option inserts, so a phone sees which
                session it is picking (SESS-33). The principal stays the plain handle. */}
            <span className="shrink-0 font-mono text-xs text-muted-foreground">
              <span>@{candidate.intelligence.handle}</span>
              {candidate.label ? <span>:{candidate.label}</span> : null}
            </span>
          </div>
        );
      })}
    </div>
  );
}
