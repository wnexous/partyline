import { EyeIcon, PaperclipIcon, SendHorizontalIcon, UsersRoundIcon, XIcon } from "lucide-react";
import { type KeyboardEvent, useId, useMemo, useRef, useState } from "react";
import { platformLabel } from "@/components/message-item";
import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { isApiError } from "@/lib/api/client";
import { usePostMessage } from "@/lib/api/queries";
import {
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
 * MaxTextLength (MSG-02) cannot be sent; files alone can. Recipients default to every chat with
 * outbound on (DLV-03); picking chats sends to exactly those (DLV-04). Recipients are chat ids
 * (`RoomMember.member_id` of chat members), not room member ids; the server rejects others (422).
 * Chats with outbound off are listen-only (DLV-05): they cannot be picked, and a 422
 * `recipients: listen-only` (the flag changed meanwhile) lands on the recipients picker.
 * In a room that does not broadcast (ROUTE-06) a post must name its chats: Send waits for a
 * picked chat. With no chat to pick, the post goes to the room's agents only.
 */

/** 422 reason for an explicit recipient whose chat member has outbound off. */
const LISTEN_ONLY = "listen-only";

const isListenOnlyError = (err: unknown) =>
  isApiError(err) && err.status === 422 && err.fields.recipients === LISTEN_ONLY;

/** The counter appears once the text gets this close to the limit. */
const COUNTER_FROM = MaxTextLength - 1_000;

export const canPost = (level: Level) => level === LevelPost || level === LevelManage;

function postErrorText(t: Translate, err: unknown): string {
  if (!isApiError(err)) return t("error.generic");
  if (err.status === 0) return t("error.network");
  if (err.status === 403) return t("composer.forbidden");
  if (err.status === 413) return t("composer.tooLarge");
  if (err.status === 429) return t("composer.rateLimited");
  if (isListenOnlyError(err)) return t("composer.recipientsListenOnly");
  if (err.status === 422) return err.message || t("error.generic");
  return t("error.generic");
}

export interface ComposerProps {
  roomId: string;
  roomName: string;
  level: Level;
  members?: RoomMember[];
  /** Room.broadcast; false asks for an explicit chat (ROUTE-06). */
  broadcast?: boolean;
}

export function Composer({ roomId, roomName, level, members, broadcast = true }: ComposerProps) {
  const { t } = useI18n();
  if (!canPost(level)) {
    return (
      <div className="flex items-center gap-2.5 border-t bg-muted/50 px-4 py-3 text-sm text-muted-foreground md:px-6">
        <EyeIcon className="size-4 shrink-0" aria-hidden="true" />
        <p>{t("composer.readOnly")}</p>
      </div>
    );
  }
  return (
    <ComposerForm roomId={roomId} roomName={roomName} members={members} broadcast={broadcast} />
  );
}

function ComposerForm({
  roomId,
  roomName,
  members,
  broadcast,
}: {
  roomId: string;
  roomName: string;
  members?: RoomMember[];
  broadcast: boolean;
}) {
  const { t, locale } = useI18n();
  const post = usePostMessage(roomId);
  const [text, setText] = useState("");
  const [files, setFiles] = useState<File[]>([]);
  const [recipients, setRecipients] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [recipientsInvalid, setRecipientsInvalid] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const textRef = useRef<HTMLTextAreaElement>(null);
  const counterId = useId();
  const errorId = useId();
  const explicitId = useId();

  const chats = useMemo(() => (members ?? []).filter((m) => m.kind === "chat"), [members]);
  // Only chats with outbound on can be explicit recipients (DLV-05).
  const pickable = useMemo(() => chats.filter((c) => c.outbound), [chats]);
  const chosen = recipients.filter((id) => pickable.some((c) => c.member_id === id));
  // ROUTE-06: a room that does not broadcast answers chats only when they are named.
  const needsChat = !broadcast && pickable.length > 0;
  const length = textLength(text);
  const tooLong = length > MaxTextLength;
  const trimmed = text.trim();
  const canSend =
    !tooLong &&
    (trimmed.length > 0 || files.length > 0) &&
    (!needsChat || chosen.length > 0) &&
    !post.isPending;
  const fmt = (n: number) => new Intl.NumberFormat(locale).format(n);

  const send = () => {
    if (!canSend) return;
    setError(null);
    setRecipientsInvalid(false);
    post.mutate(
      {
        text: trimmed,
        ...(chosen.length ? { recipients: chosen } : {}),
        ...(files.length ? { files } : {}),
      },
      {
        onSuccess: () => {
          setText("");
          setFiles([]);
          textRef.current?.focus();
        },
        onError: (err) => {
          setError(postErrorText(t, err));
          setRecipientsInvalid(isListenOnlyError(err));
        },
      },
    );
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== "Enter" || e.shiftKey || e.nativeEvent.isComposing) return;
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
      {needsChat ? (
        <p id={explicitId} className="mb-2 text-xs text-muted-foreground">
          {t("composer.explicitHint")}
        </p>
      ) : null}
      <div
        className={cn(
          "rounded-lg border border-input bg-card shadow-xs transition-[border-color,box-shadow] focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/25",
          tooLong &&
            "border-destructive focus-within:border-destructive focus-within:ring-destructive/20",
        )}
      >
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
        <textarea
          ref={textRef}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          rows={1}
          aria-label={t("composer.label", { room: roomName })}
          aria-invalid={tooLong || undefined}
          aria-describedby={
            [needsChat ? explicitId : "", length >= COUNTER_FROM ? counterId : ""]
              .filter(Boolean)
              .join(" ") || undefined
          }
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
          {chats.length > 0 ? (
            <RecipientsPicker
              chats={chats}
              value={recipients}
              needsChat={needsChat}
              onChange={(ids) => {
                setRecipients(ids);
                setRecipientsInvalid(false);
              }}
              errorId={recipientsInvalid && error ? errorId : undefined}
            />
          ) : null}
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

function RecipientsPicker({
  chats,
  value,
  needsChat,
  onChange,
  errorId,
}: {
  chats: RoomMember[];
  value: string[];
  /** The room does not broadcast: no pick means no chat (ROUTE-06). */
  needsChat: boolean;
  onChange: (ids: string[]) => void;
  /** Id of the error describing the picked recipients, when the server rejected them. */
  errorId?: string;
}) {
  const { t } = useI18n();
  const selected = value.filter((id) => chats.some((c) => c.member_id === id && c.outbound));
  const label =
    selected.length === 0
      ? needsChat
        ? t("composer.toPick")
        : t("composer.toDefault")
      : selected.length === 1
        ? t("composer.toOne")
        : t("composer.toMany", { count: selected.length });
  const toggle = (id: string) =>
    onChange(selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]);

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-invalid={errorId ? true : undefined}
          aria-describedby={errorId}
          className={cn(
            "h-8 max-w-[60%] gap-1.5 px-2 text-xs font-normal text-muted-foreground",
            selected.length > 0 && "bg-tape/60 text-tape-foreground hover:bg-tape",
            errorId && "text-destructive ring-1 ring-destructive/50 ring-inset",
          )}
        >
          <UsersRoundIcon aria-hidden="true" />
          <span className="truncate">{label}</span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" side="top" className="w-72 p-0">
        <div className="border-b px-3 py-2.5">
          <p className="text-sm font-medium">{t("composer.recipientsTitle")}</p>
          <p className="text-xs text-muted-foreground">
            {needsChat ? t("composer.recipientsBodyExplicit") : t("composer.recipientsBody")}
          </p>
        </div>
        <ul className="max-h-64 overflow-y-auto py-1">
          {chats.map((c) => (
            <li key={c.id}>
              <label
                title={c.outbound ? undefined : t("composer.listenOnlyHint")}
                className={cn(
                  "flex items-center gap-2.5 px-3 py-1.5 text-sm",
                  c.outbound ? "cursor-pointer hover:bg-accent" : "cursor-not-allowed opacity-60",
                )}
              >
                <input
                  type="checkbox"
                  className="size-4 accent-primary"
                  checked={selected.includes(c.member_id)}
                  disabled={!c.outbound}
                  onChange={() => toggle(c.member_id)}
                />
                <span className="min-w-0 flex-1 truncate">{c.name}</span>
                <span className="text-xs text-muted-foreground">
                  {platformLabel(c.connector_kind)}
                  {c.outbound ? "" : ` · ${t("composer.listenOnly")}`}
                </span>
              </label>
            </li>
          ))}
        </ul>
        {selected.length > 0 ? (
          <div className="border-t p-1.5">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="w-full justify-start text-xs"
              onClick={() => onChange([])}
            >
              {needsChat ? t("composer.recipientsClear") : t("composer.recipientsReset")}
            </Button>
          </div>
        ) : null}
      </PopoverContent>
    </Popover>
  );
}
