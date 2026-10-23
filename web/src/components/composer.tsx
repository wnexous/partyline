import { EyeIcon, PaperclipIcon, SendHorizontalIcon, XIcon } from "lucide-react";
import {
  type ChangeEvent,
  type KeyboardEvent,
  type SyntheticEvent,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  MentionSuggestions,
  matchMentions,
  mentionAt,
  mentionCandidates,
  mentionOptionId,
} from "@/components/mention-suggestions";
import { Button } from "@/components/ui/button";
import { isApiError } from "@/lib/api/client";
import { useMe, usePostMessage } from "@/lib/api/queries";
import {
  type IntelligenceRef,
  type Level,
  LevelManage,
  LevelPost,
  MaxTextLength,
  type RoomMember,
} from "@/lib/api/types.gen";
import { type Translate, useI18n } from "@/lib/i18n";
import { textLength } from "@/lib/schemas";
import { cn } from "@/lib/utils";

/*
 * Message composer (UI-07). Enter sends the trimmed text, Shift+Enter adds a line. Text over
 * MaxTextLength (MSG-02) cannot be sent; files alone can. A post is a message to the room's
 * intelligences: chats are not members and there is no recipient picker any more (CID-22,
 * ROUTE-06/DLV-03..05 and their listen-only mapping are retired). The body carries the text and
 * files only. `members` still feeds the mention list below.
 *
 * Typing `@` at the start of a word suggests the conversation's participants (MEN-01..MEN-06):
 * the filtered list opens above the box, Up/Down move, Enter, Tab or a tap complete `@handle `
 * and never send, Escape or leaving the `@word` closes it, and Enter sends as usual when nothing
 * matches.
 */

/** The counter appears once the text gets this close to the limit. */
const COUNTER_FROM = MaxTextLength - 1_000;

export const canPost = (level: Level) => level === LevelPost || level === LevelManage;

function postErrorText(t: Translate, err: unknown): string {
  if (!isApiError(err)) return t("error.generic");
  if (err.status === 0) return t("error.network");
  if (err.status === 403) return t("composer.forbidden");
  if (err.status === 413) return t("composer.tooLarge");
  if (err.status === 429) return t("composer.rateLimited");
  if (err.status === 422) return err.message || t("error.generic");
  return t("error.generic");
}

export interface ComposerProps {
  roomId: string;
  roomName: string;
  level: Level;
  members?: RoomMember[];
  /**
   * @deprecated Ignored: rooms no longer send to chats, so there is no explicit-recipient mode.
   * Kept optional until the room page stops passing it (CID-22).
   */
  broadcast?: boolean;
}

export function Composer({ roomId, roomName, level, members }: ComposerProps) {
  const { t } = useI18n();
  if (!canPost(level)) {
    return (
      <div className="flex items-center gap-2.5 border-t bg-muted/50 px-4 py-3 text-sm text-muted-foreground md:px-6">
        <EyeIcon className="size-4 shrink-0" aria-hidden="true" />
        <p>{t("composer.readOnly")}</p>
      </div>
    );
  }
  return <ComposerForm roomId={roomId} roomName={roomName} members={members} />;
}

function ComposerForm({
  roomId,
  roomName,
  members,
}: {
  roomId: string;
  roomName: string;
  members?: RoomMember[];
}) {
  const { t, locale } = useI18n();
  const post = usePostMessage(roomId);
  const [text, setText] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [error, setError] = useState<string | null>(null);
  // The caret is part of the mention state: the list follows the `@word` it is in (MEN-04).
  const [caret, setCaret] = useState(0);
  const [activeIndex, setActiveIndex] = useState(0);
  // The `@word` the user closed with Escape; leaving it reopens the list on the next one.
  const [dismissedStart, setDismissedStart] = useState<number | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);
  const textRef = useRef<HTMLTextAreaElement>(null);
  // Where the caret goes once a completion re-renders the controlled textarea.
  const pendingCaret = useRef<number | null>(null);
  const counterId = useId();
  const errorId = useId();
  const mentionListId = useId();
  const viewerId = useMe().data?.id;

  const length = textLength(text);
  const tooLong = length > MaxTextLength;
  const trimmed = text.trim();
  const canSend = !tooLong && (trimmed.length > 0 || files.length > 0) && !post.isPending;
  const fmt = (n: number) => new Intl.NumberFormat(locale).format(n);

  // The mention list follows the caret: it is open while the caret sits in an `@word` that has
  // matching participants and that the user did not just close (MEN-01, MEN-04, MEN-05).
  const mention = mentionAt(text, caret);
  const query = mention?.query ?? null;
  const mentionCandidatesShown = useMemo(
    () => mentionCandidates(members ?? [], viewerId),
    [members, viewerId],
  );
  const candidates = useMemo(
    () => (query === null ? [] : matchMentions(mentionCandidatesShown, query)),
    [mentionCandidatesShown, query],
  );
  const open = mention !== null && candidates.length > 0 && mention.start !== dismissedStart;
  // A shorter match set keeps the highlight inside it.
  const active = candidates.length > 0 ? Math.min(activeIndex, candidates.length - 1) : 0;
  const activeCandidate = candidates[active];
  const mentionStart = mention?.start ?? null;

  // Leaving the `@word` (or sending) resets the Escape dismissal; the next mention opens again.
  useEffect(() => {
    if (mentionStart === null) setDismissedStart(null);
  }, [mentionStart]);

  // A completion rewrites the text; put the caret after the inserted `@handle `.
  useEffect(() => {
    const position = pendingCaret.current;
    if (position === null || textRef.current?.value !== text) return;
    pendingCaret.current = null;
    const box = textRef.current;
    if (!box) return;
    box.focus();
    box.setSelectionRange(position, position);
    setCaret(position);
  }, [text]);

  const send = () => {
    if (!canSend) return;
    setError(null);
    post.mutate(
      {
        text: trimmed,
        ...(files.length ? { files } : {}),
      },
      {
        onSuccess: () => {
          setText("");
          setFiles([]);
          textRef.current?.focus();
        },
        onError: (err) => setError(postErrorText(t, err)),
      },
    );
  };

  // Replaces the `@word` with `@handle ` (MEN-03); it never sends the message itself.
  const completeMention = (candidate: IntelligenceRef) => {
    if (!mention) return;
    const inserted = `@${candidate.handle} `;
    const position = mention.start + inserted.length;
    pendingCaret.current = position;
    setText(`${text.slice(0, mention.start)}${inserted}${text.slice(mention.end)}`);
    setCaret(position);
    setActiveIndex(0);
    setDismissedStart(null);
  };

  // The caret is read from every event that can move it, so the list closes as soon as it leaves
  // the `@word` (MEN-04); `select` alone does not cover every browser's arrow keys.
  const syncCaret = (e: SyntheticEvent<HTMLTextAreaElement>) =>
    setCaret(e.currentTarget.selectionStart ?? 0);

  const onChange = (e: ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value);
    setCaret(e.target.selectionStart ?? e.target.value.length);
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (open && mention) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setActiveIndex(
          (current) =>
            (Math.min(current, candidates.length - 1) + step + candidates.length) %
            candidates.length,
        );
        return;
      }
      if (e.key === "Escape") {
        // MEN-04: close it, keeping the text untouched.
        e.preventDefault();
        setDismissedStart(mention.start);
        return;
      }
      if ((e.key === "Enter" && !e.shiftKey) || (e.key === "Tab" && !e.shiftKey)) {
        // MEN-03: the first candidate is active unless the user moved with Up/Down.
        if (!activeCandidate) return;
        e.preventDefault();
        completeMention(activeCandidate);
        return;
      }
    }
    if (e.key !== "Enter" || e.shiftKey) return;
    e.preventDefault();
    send();
  };

  const addFiles = (list: FileList | null) => {
    if (!list?.length) return;
    const picked = Array.from(list);
    setFiles((prev) => [...prev, ...picked]);
    if (fileInput.current) fileInput.current.value = "";
  };

  return (
    <form
      className="border-t bg-background px-3 pt-2.5 pb-3 md:px-6"
      onSubmit={(e) => {
        e.preventDefault();
        send();
      }}
    >
      {error ? (
        <p id={errorId} role="alert" className="mb-2 text-sm text-destructive">
          {error}
        </p>
      ) : null}
      <div
        className={cn(
          "relative rounded-lg border border-input bg-card shadow-xs transition-[border-color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/25",
          tooLong &&
            "border-destructive focus-within:border-destructive focus-within:ring-destructive/20",
        )}
      >
        {open ? (
          <MentionSuggestions
            id={mentionListId}
            candidates={candidates}
            activeIndex={active}
            viewerId={viewerId}
            onPick={completeMention}
          />
        ) : null}
        {files.length > 0 ? (
          <ul className="flex flex-wrap gap-1.5 px-3 pt-2.5">
            {files.map((f) => (
              <li
                key={`${f.name}-${f.size}-${f.lastModified}`}
                className="inline-flex max-w-full items-center gap-1 rounded-md border bg-muted py-0.5 pr-0.5 pl-2 text-xs"
              >
                <span className="truncate">{f.name}</span>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  aria-label={t("composer.removeFile", { name: f.name })}
                  onClick={() => setFiles((prev) => prev.filter((x) => x !== f))}
                >
                  <XIcon aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
        {/* biome-ignore lint/a11y/useAriaPropsSupportedByRole: while the mention list is open the box has role combobox */}
        <textarea
          ref={textRef}
          value={text}
          onChange={onChange}
          onKeyDown={onKeyDown}
          onSelect={syncCaret}
          onKeyUp={syncCaret}
          onClick={syncCaret}
          onFocus={syncCaret}
          rows={1}
          aria-label={t("composer.label", { room: roomName })}
          aria-invalid={tooLong || undefined}
          /* MEN-06: with the list open the box is the popup's combobox and names the active
             option; closed it stays the plain textarea the rest of the app looks for. */
          role={open ? "combobox" : undefined}
          aria-expanded={open ? true : undefined}
          aria-controls={open ? mentionListId : undefined}
          aria-autocomplete={open ? "list" : undefined}
          aria-haspopup={open ? "listbox" : undefined}
          aria-activedescendant={
            open && activeCandidate ? mentionOptionId(mentionListId, activeCandidate.id) : undefined
          }
          aria-describedby={length >= COUNTER_FROM ? counterId : undefined}
          placeholder={t("composer.placeholder", { room: roomName })}
          className="block max-h-56 min-h-11 w-full resize-none bg-transparent px-3 pt-2.5 pb-1 text-[0.9375rem] leading-relaxed outline-none [field-sizing:content] placeholder:text-muted-foreground"
        />
        <div className="flex items-center gap-1 px-1.5 pb-1.5">
          {/* The file input itself takes focus; the label draws it as an icon button. */}
          <label
            title={t("composer.attach")}
            className="relative inline-flex size-8 cursor-pointer items-center justify-center rounded-md text-muted-foreground transition-colors focus-within:ring-[3px] focus-within:ring-ring/50 hover:bg-accent hover:text-accent-foreground"
          >
            <input
              ref={fileInput}
              type="file"
              multiple
              className="sr-only"
              aria-label={t("composer.attach")}
              onChange={(e) => addFiles(e.target.files)}
            />
            <PaperclipIcon className="size-4" aria-hidden="true" />
          </label>
          <span className="ml-auto hidden text-[0.7rem] text-muted-foreground lg:inline">
            {t("composer.hint")}
          </span>
          {length >= COUNTER_FROM ? (
            <span
              id={counterId}
              data-testid="composer-counter"
              className={cn(
                "ml-auto font-mono text-[0.7rem] tabular lg:ml-2",
                tooLong ? "font-semibold text-destructive" : "text-muted-foreground",
              )}
            >
              {fmt(length)} / {fmt(MaxTextLength)}
            </span>
          ) : null}
          <Button
            type="submit"
            size="icon-sm"
            disabled={!canSend}
            aria-label={t("composer.send")}
            className={cn(length < COUNTER_FROM && "ml-auto lg:ml-2")}
          >
            <SendHorizontalIcon aria-hidden="true" />
          </Button>
        </div>
      </div>
    </form>
  );
}
