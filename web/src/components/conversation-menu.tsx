import { useNavigate, useRouter } from "@tanstack/react-router";
import { CheckCheckIcon, LogOutIcon, Trash2Icon, UserRoundIcon } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ActionMenu, type MenuAction } from "@/components/action-menu";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { useProfilePanel } from "@/hooks/use-profile-panel";
import { useDeleteRoom, useLeaveRoom, useMarkRead, useMe, useRoomMembers } from "@/lib/api/queries";
import {
  type Conversation,
  ConversationKindDM,
  ConversationKindGroup,
  LevelManage,
  LevelPost,
} from "@/lib/api/types.gen";
import { type Translate, useT } from "@/lib/i18n";

/*
 * The actions of a conversation of the sidebar (ACT-03, ACT-04, ROOM-01): Mark as read where
 * something waits, View profile for a DM, Leave group for a member and Delete group for a manager,
 * the last two after a question. They use the routes that were already there (`PUT /rooms/{id}/read`,
 * `DELETE /rooms/{id}/members/{mid}`, `DELETE /rooms/{id}`) and the rules those routes have: a
 * member takes their own member row out to leave (manage is not needed for that), a group is
 * deleted with `manage`, and a DM is nobody's to delete or leave (DM-02).
 */

/** What a row's menu is open for. */
export interface ConversationMenuTarget {
  /** The row as it is now: the caller keeps it up to date while the menu is open. */
  conversation: Conversation;
  /** Where a mouse menu hangs: the pointer, or the edge of the button that opened it. */
  at: { x: number; y: number; align?: "start" | "end" };
  /** The row, or the "⋯" that opened it: it takes focus back when the menu closes. */
  opener: HTMLElement | null;
}

const managesGroup = (c: Conversation) =>
  c.kind !== ConversationKindDM && c.my_level === LevelManage;

/** A member of a group is one with a `post` level: the member row is what gives it (AD-016). */
const memberOfGroup = (c: Conversation) =>
  c.kind === ConversationKindGroup && c.my_level === LevelPost;

/** Whether a conversation has anything to offer: a row with nothing leaves the browser's own menu alone. */
export function hasConversationActions(c: Conversation): boolean {
  return (
    c.unread > 0 ||
    (c.kind === ConversationKindDM && c.counterpart !== undefined) ||
    managesGroup(c) ||
    // Leaving needs no unread and no manage: a plain member may always leave a group (ROOM-01).
    memberOfGroup(c)
  );
}

/** "Group · 3 unread messages": which kind it is, and what waits in it. */
function describe(t: Translate, c: Conversation): string {
  const kind = t(c.kind === ConversationKindDM ? "actions.kindDM" : "actions.kindGroup");
  if (c.unread === 0) return kind;
  const waiting = t(c.unread === 1 ? "conversations.unreadOne" : "conversations.unreadMany", {
    count: c.unread,
  });
  return `${kind} · ${waiting}`;
}

/** What a question is about, and who gets focus back when it is answered. */
type Question =
  | { kind: "delete"; conversation: Conversation; opener: HTMLElement | null }
  | {
      kind: "leave";
      conversation: Conversation;
      opener: HTMLElement | null;
      /** The viewer's own member row: the one the DELETE takes out. */
      memberId: string;
    };

export function ConversationMenu({
  target,
  onClose,
  touch,
}: {
  target: ConversationMenuTarget | null;
  onClose: () => void;
  touch: boolean;
}) {
  const t = useT();
  const profile = useProfilePanel();
  const router = useRouter();
  const navigate = useNavigate();
  const markRead = useMarkRead();
  const remove = useDeleteRoom();
  const leave = useLeaveRoom();
  const me = useMe().data?.id;

  // The last target stays drawn while the menu fades out.
  const last = useRef<ConversationMenuTarget | null>(null);
  if (target) last.current = target;
  const shown = last.current;
  const c = shown?.conversation;
  const manages = c !== undefined && managesGroup(c);
  // Leaving a group is taking the viewer's own member row out, so the row has to be looked up.
  const members = useRoomMembers(c?.id ?? "", target !== null && c?.kind !== ConversationKindDM);
  const own =
    c && c.kind !== ConversationKindDM && me
      ? members.data?.find((m) => m.kind === "user" && m.member_id === me)
      : undefined;

  const [question, setQuestion] = useState<Question | null>(null);
  const [asking, setAsking] = useState(false);
  // A choice that opens a dialog or a panel takes focus there; the menu must not take it back.
  const handedOver = useRef(false);
  const ask = (next: Question) => {
    handedOver.current = true;
    setQuestion(next);
    setAsking(true);
  };
  const returnFocus = (event: Event) => {
    event.preventDefault();
    if (handedOver.current) {
      handedOver.current = false;
      return;
    }
    shown?.opener?.focus({ preventScroll: true });
  };
  /** The open conversation is gone: back to the list. */
  const leaveIfOpen = (id: string) => {
    if (router.state.location.pathname === `/rooms/${id}`) void navigate({ to: "/" });
  };

  const actions: MenuAction[] = [];
  if (c && shown) {
    if (c.unread > 0) {
      actions.push({
        id: "read",
        label: t("actions.markRead"),
        icon: CheckCheckIcon,
        onSelect: () =>
          markRead.mutate(
            { roomId: c.id },
            { onError: () => toast.error(t("actions.markReadFailed", { name: c.title })) },
          ),
      });
    }
    if (c.kind === ConversationKindDM && c.counterpart) {
      const who = c.counterpart.id;
      actions.push({
        id: "profile",
        label: t("actions.viewProfile"),
        icon: UserRoundIcon,
        onSelect: () => {
          handedOver.current = true;
          profile.open(who, shown.opener);
        },
      });
    }
    if (own) {
      actions.push({
        id: "leave",
        label: t("actions.leaveGroup"),
        icon: LogOutIcon,
        onSelect: () =>
          ask({ kind: "leave", conversation: c, opener: shown.opener, memberId: own.id }),
      });
    }
    if (manages) {
      actions.push({
        id: "delete",
        label: t("actions.deleteGroup"),
        icon: Trash2Icon,
        destructive: true,
        onSelect: () => ask({ kind: "delete", conversation: c, opener: shown.opener }),
      });
    }
  }

  const confirm = () => {
    if (!question) return;
    const { conversation } = question;
    const name = conversation.title;
    if (question.kind === "leave") {
      leave.mutate(
        { roomId: conversation.id, memberId: question.memberId },
        {
          onSuccess: () => {
            setAsking(false);
            toast(t("actions.left", { name }));
            leaveIfOpen(conversation.id);
          },
          onError: () => toast.error(t("actions.leaveFailed", { name })),
        },
      );
      return;
    }
    remove.mutate(conversation.id, {
      onSuccess: () => {
        setAsking(false);
        toast(t("room.deleted", { name }));
        leaveIfOpen(conversation.id);
      },
      onError: () => toast.error(t("actions.deleteFailed", { name })),
    });
  };

  return (
    <>
      {shown && c ? (
        <ActionMenu
          open={target !== null}
          onClose={onClose}
          touch={touch}
          title={c.title}
          description={describe(t, c)}
          at={shown.at}
          actions={actions}
          onCloseFocus={returnFocus}
        />
      ) : null}
      {question ? (
        <ConfirmDialog
          open={asking}
          onOpenChange={setAsking}
          title={t(question.kind === "leave" ? "actions.leaveTitle" : "actions.deleteTitle", {
            name: question.conversation.title,
          })}
          body={t(question.kind === "leave" ? "actions.leaveBody" : "actions.deleteBody")}
          confirmLabel={t(question.kind === "leave" ? "actions.leaveGroup" : "actions.deleteGroup")}
          pending={question.kind === "leave" ? leave.isPending : remove.isPending}
          onConfirm={confirm}
          onCloseFocus={(event) => {
            event.preventDefault();
            question.opener?.focus({ preventScroll: true });
          }}
        />
      ) : null}
    </>
  );
}
