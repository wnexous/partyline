import { type ReactNode, type Ref, useRef, useState } from "react";
import {
  AiStatusLine,
  IntelligenceHandle,
  IntelligenceName,
  ProfileLink,
} from "@/entities/intelligence";
import { ConversationAvatar, conversationTitle, counterpartOf } from "@/entities/room";
import { ConversationKindDM, type Room } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { useTouchFirst } from "@/shared/lib/device";
import { cn } from "@/shared/lib/utils";
import { ActionMenu, type MenuAction } from "@/shared/ui/molecules/action-menu";
import { BackToConversations } from "@/shared/ui/molecules/back-link";
import { RowMenuButton } from "@/shared/ui/molecules/row-menu-button";

/**
 * A finger's target on a phone's one-row header: 44 px whatever the system font, since a target
 * sized in rem grows with the text and would make the row as tall as two at 150%.
 */
const TOUCH = "size-[44px]";

/**
 * The conversation page's header: avatar and title; for a DM the counterpart's @handle and, for
 * an AI, whether it is online and in which app (the face and the name open its profile panel,
 * PRF-01); for a group its description. A DM the viewer only reads through an AI has no
 * counterpart: both participants are named (PROF-09). Nothing beside the title says whether the
 * counterpart is an AI or a person (AD-024). `actions` are the page's buttons, `back` the way back
 * on a phone.
 *
 * A phone keeps the header to one row, so the timeline has the height the on-screen keyboard and
 * the composer leave (UI-24): `menu` set, the buttons give way to one "More options" button that
 * opens the same actions in the app's action menu, and a short window (the keyboard open) drops
 * the line under the title. A wide screen shows `actions` as they are.
 */
export function ConversationHeader({
  room,
  viewerId,
  back,
  actions,
  menu,
  menuRef,
}: {
  room: Room;
  viewerId: string;
  back: boolean;
  actions: ReactNode;
  /** The actions behind "More options", in place of `actions`: set on a phone. */
  menu?: MenuAction[];
  /** The "More options" button, for a panel one of its actions opens to give focus back to. */
  menuRef?: Ref<HTMLButtonElement>;
}) {
  const t = useT();
  const title = conversationTitle(room, viewerId, t);
  const isDM = room.kind === ConversationKindDM;
  const counterpart = counterpartOf(room, viewerId);
  const face = isDM ? (counterpart ?? room.participants[0]) : undefined;
  const [first, second] = room.participants;
  const between = isDM && !counterpart && first && second ? ([first, second] as const) : null;
  const compact = menu !== undefined;

  return (
    <header
      data-testid="conversation-header"
      className={cn(
        "flex items-center border-b px-4 md:px-6",
        // One row on a phone: its buttons hug the edges and the gaps are tight, so the name has
        // the room (a third of a 360 px phone with the system font at 150%).
        compact ? "flex-nowrap gap-1.5 py-0.5" : "flex-wrap gap-2 py-2.5",
      )}
    >
      {back ? <BackToConversations className={cn("-ml-3", compact && TOUCH)} /> : null}
      {face ? (
        // The face of a DM opens that intelligence's profile panel (PRF-01); a group has no profile.
        <ProfileLink
          id={face.id}
          aria-label={t("message.openProfile", { name: face.display_name })}
          className="shrink-0 rounded-full focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
        >
          <ConversationAvatar
            variant="face"
            counterpart={face}
            group={{ id: room.id, name: room.name, avatarUrl: room.avatar_url }}
            size={compact ? "default" : "lg"}
            decorative
          />
        </ProfileLink>
      ) : (
        <ConversationAvatar
          variant="face"
          group={{ id: room.id, name: room.name, avatarUrl: room.avatar_url }}
          size={compact ? "default" : "lg"}
          decorative
        />
      )}
      {/* A base of 6rem: when the actions would squeeze the title to a letter (large text on a
          narrow window), they wrap to a row of their own instead. A phone has one button there. */}
      <div className={cn("min-w-0", compact ? "flex-1" : "flex-[1_1_6rem]")}>
        <div className="flex min-w-0 items-center gap-2">
          {between ? (
            <h1
              className={cn(
                "flex min-w-0 flex-wrap items-center gap-x-1.5 text-lg tracking-[-0.015em]",
                compact && "leading-tight",
              )}
            >
              <IntelligenceName intelligence={between[0]} viewerId={viewerId} profileLink />
              <span className="font-normal text-muted-foreground">{t("conversation.and")}</span>
              <IntelligenceName intelligence={between[1]} viewerId={viewerId} profileLink />
            </h1>
          ) : (
            <h1
              className={cn(
                "truncate text-lg font-semibold tracking-[-0.015em]",
                compact && "leading-tight",
              )}
            >
              {counterpart ? (
                <ProfileLink
                  id={counterpart.id}
                  className="rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
                >
                  {title}
                </ProfileLink>
              ) : (
                title
              )}
            </h1>
          )}
        </div>
        <p
          className={cn(
            "flex min-w-0 items-center gap-x-2 text-sm text-muted-foreground",
            // With the keyboard open the window is short: the name alone, the details are a tap away.
            compact && "leading-tight [@media(max-height:499px)]:hidden",
          )}
        >
          {isDM ? (
            counterpart ? (
              <>
                {counterpart.handle ? (
                  <IntelligenceHandle handle={counterpart.handle} className="truncate" />
                ) : null}
                {counterpart.role === "ai" ? (
                  <AiStatusLine id={counterpart.id} appClassName="max-sm:hidden" />
                ) : null}
              </>
            ) : (
              <span className="truncate">{t("conversation.directMessage")}</span>
            )
          ) : room.description ? (
            <span className="truncate">{room.description}</span>
          ) : null}
        </p>
      </div>
      {compact ? (
        menu.length > 0 ? (
          <MoreOptions title={title} actions={menu} buttonRef={menuRef} />
        ) : null
      ) : actions ? (
        <div className="ml-auto flex shrink-0 items-center gap-2">{actions}</div>
      ) : null}
    </header>
  );
}

/**
 * The header's "More options" on a phone: the app's action menu (a sheet on a touch device, a menu
 * with a mouse), named after the conversation. Focus goes back to the button when it closes, and a
 * choice runs only then, so the panel or sheet it opens takes focus from the button and gives it
 * back there.
 */
function MoreOptions({
  title,
  actions,
  buttonRef,
}: {
  title: string;
  actions: MenuAction[];
  buttonRef?: Ref<HTMLButtonElement>;
}) {
  const t = useT();
  const touch = useTouchFirst();
  const [open, setOpen] = useState(false);
  const at = useRef<{ x: number; y: number; align: "end" }>({ x: 0, y: 0, align: "end" });
  const opener = useRef<HTMLElement | null>(null);
  const chosen = useRef<(() => void) | null>(null);
  const label = t("conversation.moreOptions");
  return (
    <div className="-mr-3 ml-auto flex shrink-0">
      <RowMenuButton
        ref={buttonRef}
        label={label}
        expanded={open}
        onOpen={(where, button) => {
          at.current = where;
          opener.current = button;
          chosen.current = null;
          setOpen(true);
        }}
        className={cn(TOUCH, "pointer-coarse:size-[44px] [&_svg:not([class*='size-'])]:size-5")}
      />
      <ActionMenu
        open={open}
        onClose={() => setOpen(false)}
        touch={touch}
        title={title}
        label={label}
        at={at.current}
        actions={actions.map((a) => ({
          ...a,
          onSelect: a.onSelect
            ? () => {
                chosen.current = a.onSelect ?? null;
              }
            : undefined,
        }))}
        onCloseFocus={(event) => {
          event.preventDefault();
          opener.current?.focus({ preventScroll: true });
          const run = chosen.current;
          chosen.current = null;
          run?.();
        }}
      />
    </div>
  );
}
