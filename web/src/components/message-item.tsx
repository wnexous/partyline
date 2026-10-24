import {
  BotIcon,
  CornerDownRightIcon,
  FileIcon,
  InfoIcon,
  type LucideIcon,
  MessageCircleIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  UserIcon,
} from "lucide-react";
import { lazy, memo, Suspense } from "react";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { ProfileLink } from "@/components/profile-link";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { attachmentUrl } from "@/lib/api/queries";
import type {
  Attachment,
  AuthorKind,
  IntelligenceRef,
  Message,
  RoomMember,
} from "@/lib/api/types.gen";
import { type MessageKey, type Translate, useI18n } from "@/lib/i18n";
import { formatAbsolute, formatClock } from "@/lib/time";
import { cn } from "@/lib/utils";

const Markdown = lazy(() => import("@/components/markdown"));

/*
 * One timeline row (UI-05). Message content is data: plain text for people and chats, Markdown
 * without raw HTML for agents (UI-09). Chat senders always carry their trust badge, because that
 * is what tells a human whether agents will act on the text.
 */

export const AUTHOR_KIND: Record<
  AuthorKind,
  { icon: LucideIcon; label: MessageKey; tile: string }
> = {
  agent: { icon: BotIcon, label: "message.kind.agent", tile: "bg-primary text-primary-foreground" },
  chat: {
    icon: MessageCircleIcon,
    label: "message.kind.chat",
    tile: "bg-tape text-tape-foreground",
  },
  user: {
    icon: UserIcon,
    label: "message.kind.user",
    tile: "bg-secondary text-foreground ring-1 ring-border ring-inset",
  },
  system: { icon: InfoIcon, label: "message.kind.system", tile: "bg-muted text-muted-foreground" },
};

const PLATFORMS: Record<string, string> = {
  telegram: "Telegram",
  whatsapp: "WhatsApp",
  discord: "Discord",
  webhook: "Webhook",
};

export const platformLabel = (kind?: string) => (kind ? (PLATFORMS[kind] ?? kind) : "");

/** A platform's display name; kinds without a known label are capitalized, like the server's. */
export function platformName(kind?: string): string {
  const label = platformLabel(kind);
  return label.charAt(0).toUpperCase() + label.slice(1);
}

/**
 * A chat's name (CTX-07): its title, else "<Platform> chat", matching the names the server gives
 * rooms routed from untitled chats (connectors.PlatformLabel).
 */
export function chatTitle(t: Translate, title: string | undefined, platform?: string): string {
  const trimmed = title?.trim();
  if (trimmed) return trimmed;
  const name = platformName(platform);
  return name ? t("chat.untitled", { platform: name }) : t("message.kind.chat");
}

/** Images the server serves inline (others download); SVG is excluded on purpose. */
export const INLINE_IMAGES = new Set(["image/png", "image/jpeg", "image/gif", "image/webp"]);
/** An image the timeline shows inline and the viewer can open: stored, and one the server serves inline. */
export const isViewableImage = (a: Attachment) =>
  a.status === "stored" && INLINE_IMAGES.has(a.mime);
/** Reserved box for inline images (Attachment has no width/height). */
const IMAGE_BOX = { width: 320, height: 240 } as const;
const IMAGE_BOX_RATIO = "4 / 3";

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
function authorProfile(message: Message): IntelligenceRef | null {
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
}

export const MessageItem = memo(function MessageItem({
  message,
  meId,
  members,
  replyTarget,
  grouped = false,
  fresh = false,
  onJumpTo,
  onOpenImage,
}: MessageItemProps) {
  const { t, locale } = useI18n();
  const kind = AUTHOR_KIND[message.author_kind] ?? AUTHOR_KIND.system;
  const time = (
    <time
      dateTime={message.created_at}
      title={formatAbsolute(message.created_at, locale)}
      className="font-mono text-[0.7rem] text-muted-foreground tabular"
    >
      {formatClock(message.created_at, locale)}
    </time>
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
        <span className="sr-only">{t(kind.label)}</span>
        <span className="min-w-0 text-center text-pretty">
          <MessageText message={message} />
        </span>
        {time}
        <span aria-hidden="true" className="h-px flex-1 bg-border" />
      </article>
    );
  }

  const chatMember = message.author_kind === "chat" ? members?.get(message.author_id ?? "") : null;
  const platform = platformLabel(chatMember?.connector_kind);
  const profile = authorProfile(message);

  return (
    <article
      data-testid={`message-${message.seq}`}
      className={cn(
        "group/message relative grid grid-cols-[2rem_minmax(0,1fr)] gap-x-3 px-4 transition-colors hover:bg-accent/35 md:px-6",
        grouped ? "py-0.5" : "pt-2.5 pb-1",
        fresh && "animate-tape-settle",
      )}
    >
      <div className="flex justify-center">
        {grouped ? (
          <span className="absolute top-1 right-4 opacity-0 transition-opacity group-hover/message:opacity-100 group-focus-within/message:opacity-100 md:right-6">
            {time}
          </span>
        ) : profile ? (
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
            {t(kind.label)} {authorDisplayName(message)}
          </span>
        ) : (
          <header className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="sr-only">{t(kind.label)}</span>
            {profile ? (
              <IntelligenceName
                intelligence={
                  // A chat row names its platform in its own pill below.
                  message.author_kind === "chat" ? { ...profile, platform: undefined } : profile
                }
                viewerId={meId}
                profileLink
              />
            ) : (
              <span className="truncate font-semibold tracking-[-0.01em]">
                {authorDisplayName(message)}
              </span>
            )}
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
            {time}
          </header>
        )}

        {replyTarget !== undefined ? (
          <ReplyContext target={replyTarget} onJumpTo={onJumpTo} />
        ) : null}

        <div className="text-[0.9375rem] leading-relaxed">
          {message.author_kind === "agent" && message.text ? (
            <Suspense fallback={<PlainText text={message.text} />}>
              <Markdown text={message.text} />
            </Suspense>
          ) : (
            <MessageText message={message} />
          )}
        </div>

        {message.attachments.length > 0 ? (
          <ul className="mt-1.5 flex flex-wrap gap-2">
            {message.attachments.map((a) => (
              <li key={a.id} className="max-w-full">
                <AttachmentView attachment={a} onOpenImage={onOpenImage} />
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </article>
  );
});

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
const TRUST: Record<
  Trust,
  { icon: LucideIcon; label: MessageKey; hint: MessageKey; className: string }
> = {
  trusted: {
    icon: ShieldCheckIcon,
    label: "message.trusted",
    hint: "message.trustedHint",
    className: "bg-tape text-tape-foreground ring-1 ring-tape-strong/50 ring-inset",
  },
  verified: {
    icon: ShieldCheckIcon,
    label: "message.verified",
    hint: "message.verifiedHint",
    className: "text-foreground ring-1 ring-tape-strong/50 ring-inset",
  },
  untrusted: {
    icon: ShieldAlertIcon,
    label: "message.untrusted",
    hint: "message.untrustedHint",
    className: "text-foreground outline-1 -outline-offset-1 outline-tape-strong outline-dashed",
  },
};

export function TrustBadge({ trust }: { trust: Trust }) {
  const { t } = useI18n();
  const cfg = TRUST[trust];
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          className={cn(
            "inline-flex items-center gap-1 self-center rounded-[3px] px-1.5 py-px text-[0.7rem] font-semibold",
            cfg.className,
          )}
        >
          <cfg.icon className="size-3" aria-hidden="true" />
          <span>{t(cfg.label)}</span>
          <span className="sr-only">. {t(cfg.hint)}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64 text-pretty">{t(cfg.hint)}</TooltipContent>
    </Tooltip>
  );
}

function ReplyContext({
  target,
  onJumpTo,
}: {
  target: Message | null;
  onJumpTo?: (id: string) => void;
}) {
  const { t } = useI18n();
  const body = target ? (
    <>
      <span className="font-medium text-foreground/80">{authorDisplayName(target)}</span>{" "}
      <span className="truncate">{target.text || ""}</span>
    </>
  ) : (
    <span>{t("message.replyMissing")}</span>
  );
  const className =
    "my-0.5 flex max-w-full min-w-0 items-center gap-1.5 truncate text-left text-xs text-muted-foreground";
  return target && onJumpTo ? (
    <button
      type="button"
      data-testid="reply-context"
      className={cn(className, "rounded-sm hover:text-foreground focus-visible:outline-2")}
      onClick={() => onJumpTo(target.id)}
      aria-label={t("message.replyTo", { name: authorDisplayName(target) })}
    >
      <CornerDownRightIcon className="size-3 shrink-0" aria-hidden="true" />
      {body}
    </button>
  ) : (
    <p data-testid="reply-context" className={className}>
      <CornerDownRightIcon className="size-3 shrink-0" aria-hidden="true" />
      {body}
    </p>
  );
}

export function formatBytes(size: number, locale: string): string {
  const units = ["B", "KB", "MB", "GB"];
  let value = size;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const digits = unit === 0 || value >= 10 ? 0 : 1;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: digits }).format(value)} ${units[unit]}`;
}

function AttachmentView({
  attachment: a,
  onOpenImage,
}: {
  attachment: Attachment;
  onOpenImage: (attachmentId: string) => void;
}) {
  const { t, locale } = useI18n();
  const url = attachmentUrl(a.id);
  if (a.status !== "stored") {
    return (
      <span className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-dashed px-2 py-1 text-xs text-muted-foreground">
        <FileIcon className="size-3.5 shrink-0" aria-hidden="true" />
        <span className="truncate">{a.name}</span>
        <span>
          ·{" "}
          {a.status === "too_large"
            ? t("message.attachmentTooLarge")
            : t("message.attachmentUnavailable")}
        </span>
      </span>
    );
  }
  if (INLINE_IMAGES.has(a.mime)) {
    return (
      // The image opens in the viewer inside the app (IMG-01), which hands focus back to this
      // button (`data-attachment-id` finds it again).
      <button
        type="button"
        data-attachment-id={a.id}
        aria-haspopup="dialog"
        aria-label={t("message.openImage", { name: a.name })}
        onClick={() => onOpenImage(a.id)}
        // Attachment carries no pixel size: a fixed 4:3 box keeps the row height stable while the
        // image loads (no layout shift, steady virtualizer measurements).
        className="inline-block w-80 max-w-full cursor-zoom-in overflow-hidden rounded-md border bg-muted align-top shadow-xs"
        style={{ aspectRatio: IMAGE_BOX_RATIO }}
      >
        <img
          src={url}
          alt={a.name}
          width={IMAGE_BOX.width}
          height={IMAGE_BOX.height}
          loading="lazy"
          decoding="async"
          className="block size-full object-contain"
        />
      </button>
    );
  }
  return (
    <a
      href={url}
      download={a.name}
      className="inline-flex max-w-full items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs shadow-xs transition-colors hover:bg-accent"
    >
      <FileIcon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden="true" />
      <span className="truncate font-medium">{a.name}</span>
      <span className="font-mono text-muted-foreground tabular">{formatBytes(a.size, locale)}</span>
    </a>
  );
}
