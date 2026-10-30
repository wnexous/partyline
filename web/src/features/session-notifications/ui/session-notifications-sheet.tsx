import { BellRingIcon } from "lucide-react";
import { useState } from "react";
import { SessionLabel } from "@/entities/intelligence";
import { errorText } from "@/shared/api/error-text";
import type { RoomNotification } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { cn } from "@/shared/lib/utils";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/ui/molecules/sheet";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { Switch } from "@/shared/ui/primitives/switch";
import { useSessionNotifications, useSetSessionNotification } from "../api/notifications";
import type { NotificationSetting } from "../model/notifications";

/**
 * The notifications of one session of an AI (SN-08): a switch per conversation, or — when `room` names
 * one — only that conversation, which is what a room's member card opens. Whoever manages the AI uses
 * it; the server refuses everyone else, so its triggers are only offered at the manage level. A phone
 * gets the bottom sheet it expects, a wide screen the side sheet.
 */
export function SessionNotificationsSheet({
  agentId,
  label,
  sessionName,
  room,
  open,
  onClose,
}: {
  agentId: string;
  /** The session's label; "" for the principal. */
  label: string;
  sessionName: string;
  /** One conversation to show and change; absent lists every conversation of the AI. */
  room?: string;
  open: boolean;
  onClose: () => void;
}) {
  const t = useT();
  const mobile = useIsMobile();
  const query = useSessionNotifications(agentId, label, open);
  const update = useSetSessionNotification(agentId, label);
  const [failed, setFailed] = useState<string | null>(null);

  const rooms = room
    ? (query.data ?? []).filter((row) => row.room_id === room)
    : (query.data ?? []);

  const set = (row: RoomNotification, next: NotificationSetting) => {
    setFailed(null);
    update.mutate(
      { roomId: row.room_id, notify: next },
      {
        onError: (err) => setFailed(errorText(t, err, "notifications")),
      },
    );
  };

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <SheetContent
        side={mobile ? "bottom" : "right"}
        className={cn("w-full gap-0 sm:max-w-md", mobile && "max-h-[85dvh] rounded-t-lg")}
      >
        <SheetHeader className="gap-1 border-b px-4 py-3 pr-12">
          <SheetTitle>
            {room ? t("notifications.roomTitle") : t("notifications.title", { name: sessionName })}
          </SheetTitle>
          <SheetDescription>{t("notifications.description")}</SheetDescription>
        </SheetHeader>
        <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
          {query.isPending ? (
            <div className="space-y-2 px-2 py-2" aria-hidden="true">
              <Skeleton className="h-14 w-full" />
              <Skeleton className="h-14 w-full" />
            </div>
          ) : query.isError ? (
            <p role="alert" className="px-2 py-3 text-sm text-destructive">
              {t("notifications.loadError")}
            </p>
          ) : rooms.length === 0 ? (
            <p className="px-2 py-3 text-sm text-muted-foreground">{t("notifications.empty")}</p>
          ) : (
            <ul aria-label={t("notifications.title", { name: sessionName })}>
              {rooms.map((row) => {
                const checked = row.notify === "on";
                return (
                  <li
                    key={row.room_id}
                    className="flex min-h-14 items-center gap-3 rounded-md px-2 py-2"
                  >
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">
                        {row.name || t("notifications.directMessage")}
                      </span>
                      <span className="block text-xs text-muted-foreground">
                        {checked ? t("notifications.on") : t("notifications.mentionsOnly")}
                        {row.unread > 0
                          ? ` · ${t("notifications.unread", { count: row.unread })}`
                          : ""}
                      </span>
                    </span>
                    <Switch
                      data-testid={`notification-switch-${row.room_id}`}
                      aria-label={t("notifications.switch", {
                        room: row.name || t("notifications.directMessage"),
                      })}
                      checked={checked}
                      disabled={update.isPending}
                      onCheckedChange={(next) => set(row, next ? "on" : "mentions")}
                    />
                  </li>
                );
              })}
            </ul>
          )}
          {failed ? (
            <p role="alert" className="px-2 py-3 text-sm text-destructive">
              {failed}
            </p>
          ) : null}
        </div>
      </SheetContent>
    </Sheet>
  );
}

/**
 * The session line of a room's member card, as a control (SN-08): the session's label opens the
 * notifications of this conversation, for whoever manages the AI. It keeps the one rendering of the
 * vocabulary (SessionLabel) and the muted marker of the session list stays beside it.
 */
export function MemberSessionNotifyButton({
  agentId,
  label,
  sessionName,
  roomId,
}: {
  agentId: string;
  label: string;
  sessionName: string;
  roomId: string;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        data-testid={`session-notify-${label || "principal"}`}
        aria-label={t("notifications.open", { session: sessionName })}
        className="inline-flex min-h-6 items-center rounded-sm font-mono text-xs outline-none hover:text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => setOpen(true)}
      >
        <SessionLabel label={label} />
      </button>
      <SessionNotificationsSheet
        agentId={agentId}
        label={label}
        sessionName={sessionName}
        room={roomId}
        open={open}
        onClose={() => setOpen(false)}
      />
    </>
  );
}

/**
 * The profile's Sessions list offers the notifications of a session (SN-08): a button that opens the
 * conversations of the AI with a switch each, for whoever manages it.
 */
export function SessionNotificationsButton({
  agentId,
  label,
  sessionName,
}: {
  agentId: string;
  label: string;
  sessionName: string;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        data-testid={`session-notifications-${label || "principal"}`}
        className="inline-flex min-h-9 items-center gap-1.5 rounded-md px-2 text-xs font-medium text-primary outline-none hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring"
        onClick={() => setOpen(true)}
      >
        <BellRingIcon className="size-3.5" aria-hidden="true" />
        {t("profile.sessionNotifications")}
      </button>
      <SessionNotificationsSheet
        agentId={agentId}
        label={label}
        sessionName={sessionName}
        open={open}
        onClose={() => setOpen(false)}
      />
    </>
  );
}
