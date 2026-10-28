import { BanIcon, InfoIcon, ReplyIcon } from "lucide-react";
import { memo, Suspense } from "react";
import type { MessageMenuTarget } from "@/components/message-menu";
import { ReactionToolbar } from "@/components/reaction-bar";
import { ReactionChips } from "@/components/reaction-chips";
import { IntelligenceAvatar, IntelligenceName, ProfileLink } from "@/entities/intelligence";
import {
  AttachmentView,
  AUTHOR_KIND,
  authorDisplayName,
  authorProfile,
  chatTitle,
  chatTrust,
  LazyMarkdown,
  platformName,
  ReplyContext,
  TrustBadge,
} from "@/entities/message";
import { QuestionBubble } from "@/features/answer-question";
import { useSwipeReply } from "@/hooks/use-swipe-reply";
import type { Message, RoomMember } from "@/shared/api/types.gen";
import { type MessageKey, useI18n } from "@/shared/i18n";
import { formatAbsolute, formatClock } from "@/shared/lib/time";
import { useLongPress } from "@/shared/lib/use-long-press";
import { cn } from "@/shared/lib/utils";
import { RowMenuButton } from "@/shared/ui/molecules/row-menu-button";

/*
 * One timeline row (UI-05). Message content is data: plain text for people and chats, Markdown
 * without raw HTML for agents (UI-09). Chat senders always carry their trust badge, because that
 * is what tells a human whether agents will act on the text.
 */

const CONTENT_KIND: Partial<Record<Message["kind"], MessageKey>> = {
  image: "message.content.image",
  video: "message.content.video",
  audio: "message.content.audio",
  document: "message.content.document",
  sticker: "message.content.sticker",
  reaction: "message.content.reaction",
  edit: "message.content.edit",
  revoke: "message.content.revoke",
  location: "message.content.location",
  contact: "message.content.contact",
  poll: "message.content.poll",
  unknown: "message.content.unknown",
};

export interface MessageItemProps {
  message: Message;
  meId: string;
  /** Room members by member id, to name chats and platforms. */
  members?: Map<string, RoomMember>;
  /** The message replied to: undefined when there is no reply, null when it is not loaded. */
  replyTarget?: Message | null;
  /** Same author as the previous row moments ago: header folds away. */
  grouped?: boolean;
  /** Arrived live while the room was open: settles from the tape highlight. */
  fresh?: boolean;
  onJumpTo?: (messageId: string) => void;
  /** Opens an image attachment in the viewer (IMG-01). */
  onOpenImage: (attachmentId: string) => void;
  /**
   * Opens the actions of this message (ACT-01, ACT-02, ACT-05). Absent when it has none: a system
   * notice, or a message with nothing to answer or copy.
   */
  onMenu?: (message: Message, at: MessageMenuTarget["at"], opener: HTMLElement) => void;
  /** A touch-first device opens them with a long press; a mouse has a "⋯" button and a right click. */
  touch?: boolean;
  /** The actions of this message are open. */
  menuOpen?: boolean;
  /**
   * Answers this message when it is dragged to the right (SWP-01). Absent where there is nothing to
   * answer in: a notice, a viewer who may not post. It only exists on a touch device.
   */
  onSwipeReply?: (message: Message) => void;
  /**
   * Reacts to this message with an emoji, or takes the viewer's reaction back (RCT-01, RCT-02). Absent
   * where the viewer may not react: the chips are then only shown. With a mouse it also puts a toolbar
   * of quick reactions on the row.
   */
  onReact?: (message: Message, emoji: string) => void;
  /**
   * The viewer's answer to the question this message asks, when the timeline holds it (QR-12): the
   * bubble shows the chosen label and stops acting.
   */
  myAnswer?: Message;
  /** The viewer may post in this conversation, so the question's quick replies act (QR-13). */
  canAnswer?: boolean;
  /** The conversation is history (CID-21): its question shows its labels and no action (QR-13). */
  frozen?: boolean;
}

/** Two handlers for one event: both gestures listen to the same finger, the long press first. */
const both =
  <E,>(first: (e: E) => void, second: (e: E) => void) =>
  (e: E) => {
    first(e);
    second(e);
  };

export const MessageItem = memo(function MessageItem({
  message,
  meId,
  members,
  replyTarget,
  grouped = false,
  fresh = false,
  onJumpTo,
  onOpenImage,
  onMenu,
  touch = false,
  menuOpen = false,
  onSwipeReply,
  onReact,
  myAnswer,
  canAnswer,
  frozen,
}: MessageItemProps) {
  const { t, locale } = useI18n();
  const press = useLongPress<HTMLElement>(
    (where) => onMenu?.(message, { x: where.x, y: where.y }, where.element),
    { enabled: onMenu !== undefined },
  );
  const withButton = onMenu !== undefined && !touch;
  const swipeable = onSwipeReply !== undefined && touch;
  const swipe = useSwipeReply<HTMLElement>(() => onSwipeReply?.(message), { enabled: swipeable });
  const kind = AUTHOR_KIND[message.author_kind] ?? AUTHOR_KIND.system;
  // A deleted message is a placeholder (DLT-10): its author and time stay, nothing it said does.
  const deleted = message.deleted_at !== undefined;
  const time = (
    <time
      dateTime={message.created_at}
      title={formatAbsolute(message.created_at, locale)}
      className="font-mono text-[0.7rem] text-muted-foreground tabular"
    >
      {formatClock(message.created_at, locale)}
    </time>
  );
  // The time, and "edited" after it for a message whose text was changed (EDT-10); the title says when.
  const stamp = (
    <>
      {time}
      {message.edited_at !== undefined && !deleted ? (
        <span
          title={t("message.editedAt", { time: formatAbsolute(message.edited_at, locale) })}
          className="text-[0.7rem] text-muted-foreground"
        >
          {t("message.edited")}
        </span>
      ) : null}
    </>
  );

  if (message.author_kind === "system") {
    return (
      <article
        data-testid={`message-${message.seq}`}
        className={cn(
          "flex items-center gap-3 px-4 py-2 text-xs text-muted-foreground md:px-6",
          fresh && "animate-tape-settle",
        )}
      >
        <span aria-hidden="true" className="h-px flex-1 bg-border" />
        <InfoIcon className="size-3.5 shrink-0" aria-hidden="true" />
        {kind.label ? <span className="sr-only">{t(kind.label)}</span> : null}
        <span className="min-w-0 text-center text-pretty">
          <MessageText message={message} />
        </span>
        {time}
        <span aria-hidden="true" className="h-px flex-1 bg-border" />
      </article>
    );
  }

  const chatMember = message.author_kind === "chat" ? members?.get(message.author_id ?? "") : null;
  const platform = platformName(chatMember?.connector_kind);
  const profile = authorProfile(message);

  return (
    <article
      data-testid={`message-${message.seq}`}
      {...press}
      onPointerDown={both(press.onPointerDown, swipe.onPointerDown)}
      onPointerMove={both(press.onPointerMove, swipe.onPointerMove)}
      onPointerUp={both(press.onPointerUp, swipe.onPointerUp)}
      onPointerCancel={both(press.onPointerCancel, swipe.onPointerCancel)}
      className={cn(
        "group/message relative grid grid-cols-[2rem_minmax(0,1fr)] gap-x-3 px-4 transition-colors hover:bg-accent/35 md:px-6",
        grouped ? "py-0.5" : "pt-2.5 pb-1",
        fresh && "animate-tape-settle",
        // A long press is the sheet's: it must not select the word under the finger or call up a
        // link's own preview. Copy text takes the whole message.
        onMenu && touch && "select-none [-webkit-touch-callout:none]",
        // The page keeps its vertical scroll and zoom; a drag across is the swipe's.
        swipeable && "touch-pan-y touch-pinch-zoom",
      )}
    >
      {swipeable ? (
        // Under the row, to its left: it shows as the row slides right, and takes the accent
        // (what you act on) once letting go would answer.
        <span
          aria-hidden="true"
          style={{
            opacity: "var(--swipe-progress, 0)",
            transform: "translateY(-50%) scale(calc(0.6 + 0.4 * var(--swipe-progress, 0)))",
          }}
          className="pointer-events-none absolute top-1/2 -left-11 grid size-9 place-items-center rounded-full bg-secondary text-muted-foreground transition-colors group-data-[armed=true]/message:bg-primary group-data-[armed=true]/message:text-primary-foreground"
        >
          <ReplyIcon className="size-4" />
        </span>
      ) : null}
      {grouped || withButton ? (
        <div className="absolute top-1 right-4 flex items-center gap-1.5 md:right-6">
          {grouped ? (
            <span className="flex items-center gap-1.5 opacity-0 transition-opacity group-focus-within/message:opacity-100 group-hover/message:opacity-100 [@media(hover:none)]:opacity-100">
              {stamp}
            </span>
          ) : null}
          {withButton && onReact ? (
            // The row's own hover and focus reveal it, and it stays while its picker is open though the
            // pointer has gone to the picker. Hidden, it must not catch the pointer over the text.
            <ReactionToolbar
              reacted={reactedBy(message, meId)}
              onReact={(emoji) => onReact(message, emoji)}
              className="pointer-events-none opacity-0 transition-opacity group-focus-within/message:pointer-events-auto group-focus-within/message:opacity-100 group-hover/message:pointer-events-auto group-hover/message:opacity-100 has-[[aria-expanded=true]]:pointer-events-auto has-[[aria-expanded=true]]:opacity-100"
            />
          ) : null}
          {withButton ? (
            <RowMenuButton
              label={t("actions.message", { name: authorDisplayName(message) })}
              expanded={menuOpen}
              onOpen={(at, button) => onMenu?.(message, at, button)}
              className="border bg-card opacity-0 shadow-xs group-focus-within/message:opacity-100 group-hover/message:opacity-100 hover:bg-accent aria-expanded:opacity-100"
            />
          ) : null}
        </div>
      ) : null}
      <div className="flex justify-center">
        {grouped ? null : profile ? (
          <ProfileLink
            id={profile.id}
            aria-label={t("message.openProfile", { name: profile.display_name })}
            className="mt-0.5 rounded-full focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
          >
            <IntelligenceAvatar intelligence={profile} decorative />
          </ProfileLink>
        ) : (
          <span
            aria-hidden="true"
            className={cn("mt-0.5 flex size-8 items-center justify-center rounded-md", kind.tile)}
          >
            <kind.icon className="size-4" strokeWidth={1.9} />
          </span>
        )}
      </div>

      <div className="min-w-0">
        {grouped ? (
          <span className="sr-only">
            {kind.label ? `${t(kind.label)} ` : ""}
            {authorDisplayName(message)}
            {message.author_kind === "agent" && message.author_label
              ? ` ${t("message.viaSession", { label: message.author_label })}`
              : ""}
          </span>
        ) : (
          <header className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            {kind.label ? <span className="sr-only">{t(kind.label)}</span> : null}
            {profile ? (
              <IntelligenceName intelligence={profile} viewerId={meId} profileLink />
            ) : (
              <span className="truncate font-semibold tracking-[-0.01em]">
                {authorDisplayName(message)}
              </span>
            )}
            {message.author_kind === "agent" && message.author_label ? (
              <span className="min-w-0 text-xs text-muted-foreground [overflow-wrap:anywhere]">
                {t("message.viaSession", { label: message.author_label })}
              </span>
            ) : null}
            {message.author_kind === "chat" && message.chat ? (
              <>
                <span className="text-xs text-muted-foreground">
                  {t("message.via", {
                    chat: chatTitle(t, message.chat.title, message.chat.platform),
                  })}
                </span>
                <span
                  data-testid="chat-platform"
                  className="rounded-sm border px-1.5 py-0.5 text-[0.68rem] leading-none font-medium text-muted-foreground"
                >
                  {platformName(message.chat.platform)}
                </span>
                <TrustBadge trust={chatTrust(message, meId)} />
              </>
            ) : message.author_kind === "chat" ? (
              <>
                <span className="text-xs text-muted-foreground">
                  {t("message.via", { chat: message.author_name })}
                  {platform ? ` · ${platform}` : ""}
                </span>
                <TrustBadge trust={chatTrust(message, meId)} />
              </>
            ) : null}
            {stamp}
          </header>
        )}

        {replyTarget !== undefined ? (
          <ReplyContext target={replyTarget} onJumpTo={onJumpTo} />
        ) : null}

        <div className="text-[0.9375rem] leading-relaxed">
          {deleted ? (
            <DeletedPlaceholder />
          ) : message.author_kind === "agent" && message.text ? (
            <Suspense fallback={<PlainText text={message.text} />}>
              <LazyMarkdown text={message.text} />
            </Suspense>
          ) : (
            <MessageText message={message} />
          )}
        </div>

        {!deleted && message.interactive ? (
          <QuestionBubble
            message={message}
            meId={meId}
            myAnswer={myAnswer}
            canAnswer={canAnswer}
            frozen={frozen}
          />
        ) : null}

        {!deleted && message.attachments.length > 0 ? (
          <ul
            className={cn(
              "mt-1.5 flex flex-wrap gap-2",
              // The time of a grouped row shows at its top right on hover and focus: with no text
              // above them, the attachments start below it, so a player's own time stays readable.
              grouped && !message.text && !CONTENT_KIND[message.kind] && "mt-5",
            )}
          >
            {message.attachments.map((a) => (
              <li key={a.id} className="max-w-full">
                <AttachmentView attachment={a} onOpenImage={onOpenImage} />
              </li>
            ))}
          </ul>
        ) : null}

        {!deleted ? (
          <ReactionChips
            reactions={message.reactions}
            meId={meId}
            onToggle={onReact ? (emoji) => onReact(message, emoji) : undefined}
          />
        ) : null}
      </div>
    </article>
  );
});

/** The emoji the viewer reacted to a message with. */
const reactedBy = (message: Message, meId: string) =>
  message.reactions.filter((r) => r.by.includes(meId)).map((r) => r.emoji);

/** What is left of a deleted message (DLT-10). */
function DeletedPlaceholder() {
  const t = useI18n().t;
  return (
    <p className="inline-flex items-center gap-1.5 text-muted-foreground italic">
      <BanIcon className="size-3.5 shrink-0" aria-hidden="true" />
      {t("message.deleted")}
    </p>
  );
}

function PlainText({ text }: { text: string }) {
  return <p className="break-words whitespace-pre-wrap">{text}</p>;
}

function MessageText({ message }: { message: Message }) {
  const { t } = useI18n();
  if (message.text) return <PlainText text={message.text} />;
  const label = CONTENT_KIND[message.kind];
  if (!label) return null;
  return <p className="text-muted-foreground italic">{t(label)}</p>;
}

/*
 * Trust has no hue to lean on, so the badges differ by shape as well as by icon and word: trusted
 * is a filled tape chip, verified is outlined, untrusted is dashed (nothing vouches for it).
 */
