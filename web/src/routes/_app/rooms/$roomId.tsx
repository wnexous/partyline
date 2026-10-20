import { createFileRoute, Link } from "@tanstack/react-router";
import {
  MessageSquareOffIcon,
  PauseIcon,
  PlusIcon,
  Settings2Icon,
  UsersRoundIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Composer } from "@/components/composer";
import { EmptyState } from "@/components/empty-state";
import { MemberList } from "@/components/member-list";
import { MemberPicker } from "@/components/member-picker";
import { MessageTimeline } from "@/components/message-timeline";
import { RoomSettingsSheet } from "@/components/room-settings-sheet";
import { VisibilityLabel } from "@/components/room-visibility";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { useIsMobile } from "@/hooks/use-mobile";
import { isApiError } from "@/lib/api/client";
import { useMe, useResumeRoom, useRoom, useRoomMembers } from "@/lib/api/queries";
import { LevelManage, type Room, type RoomMember } from "@/lib/api/types.gen";
import { useI18n, useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/rooms/$roomId")({
  component: RoomPage,
});

function RoomPage() {
  const { t } = useI18n();
  const { roomId } = Route.useParams();
  const room = useRoom(roomId);
  const members = useRoomMembers(roomId);
  const me = useMe().data;
  const isMobile = useIsMobile();
  const [panelOpen, setPanelOpen] = useState(true);
  const [sheetOpen, setSheetOpen] = useState(false);

  if (room.isError && isApiError(room.error) && room.error.status === 404) {
    return <RoomNotFound />;
  }
  if (!room.data || !me) return <RoomSkeleton />;

  const r = room.data;
  const canManage = r.my_level === LevelManage;
  const memberList = members.data ?? [];

  const toggleMembers = () => (isMobile ? setSheetOpen(true) : setPanelOpen((v) => !v));
  const panel = (
    <MembersPanel
      roomId={roomId}
      members={memberList}
      canManage={canManage}
      loading={members.isPending}
    />
  );

  return (
    <div data-fill className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-2 border-b px-4 py-2.5 md:px-6">
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-lg font-semibold tracking-[-0.015em]">{r.name}</h1>
            <p className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
              <VisibilityLabel visibility={r.visibility} className="shrink-0" />
              {r.description ? <span className="truncate">{r.description}</span> : null}
            </p>
          </div>
          <Button
            variant={!isMobile && panelOpen ? "secondary" : "ghost"}
            size="sm"
            onClick={toggleMembers}
            aria-expanded={isMobile ? sheetOpen : panelOpen}
            aria-label={t("room.membersToggle", { count: memberList.length })}
          >
            <UsersRoundIcon aria-hidden="true" />
            <span className="font-mono text-xs tabular">{memberList.length}</span>
          </Button>
          {canManage ? (
            <RoomSettingsSheet
              room={r}
              trigger={
                <Button variant="ghost" size="icon-sm" aria-label={t("settings.title")}>
                  <Settings2Icon aria-hidden="true" />
                </Button>
              }
            />
          ) : null}
        </header>
        {r.agents_paused ? <PausedBanner room={r} canManage={canManage} /> : null}
        <MessageTimeline key={roomId} roomId={roomId} meId={me.id} members={memberList} />
        <Composer
          key={`composer-${roomId}`}
          roomId={roomId}
          roomName={r.name}
          level={r.my_level}
          members={memberList}
        />
      </div>

      {!isMobile && panelOpen ? (
        <aside
          aria-label={t("members.title")}
          className="flex w-72 shrink-0 flex-col border-l bg-sidebar/40 xl:w-80"
        >
          {panel}
        </aside>
      ) : null}
      {isMobile ? (
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
          <SheetContent className="w-[min(22rem,100vw)] gap-0 p-0">
            <SheetHeader className="sr-only">
              <SheetTitle>{t("members.title")}</SheetTitle>
              <SheetDescription>{r.name}</SheetDescription>
            </SheetHeader>
            <aside aria-label={t("members.title")} className="flex min-h-0 flex-1 flex-col">
              {panel}
            </aside>
          </SheetContent>
        </Sheet>
      ) : null}
    </div>
  );
}

function MembersPanel({
  roomId,
  members,
  canManage,
  loading,
}: {
  roomId: string;
  members: RoomMember[];
  canManage: boolean;
  loading: boolean;
}) {
  const t = useT();
  return (
    <>
      <div className="flex h-12 items-center justify-between gap-2 border-b px-4">
        <h2 className="text-sm font-semibold">{t("members.title")}</h2>
        {canManage ? (
          <MemberPicker
            roomId={roomId}
            members={members}
            trigger={
              <Button variant="outline" size="xs" className="mr-6 md:mr-0">
                <PlusIcon aria-hidden="true" />
                {t("picker.title")}
              </Button>
            }
          />
        ) : null}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {loading ? (
          <div className="space-y-3 p-4" aria-hidden="true">
            <Skeleton className="h-4 w-36" />
            <Skeleton className="h-4 w-44" />
          </div>
        ) : (
          <MemberList roomId={roomId} members={members} canManage={canManage} />
        )}
      </div>
    </>
  );
}

function PausedBanner({ room, canManage }: { room: Room; canManage: boolean }) {
  const t = useT();
  const resume = useResumeRoom(room.id);
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-warning/40 bg-warning/15 px-4 py-2.5 text-sm md:px-6"
    >
      <PauseIcon className="size-4 shrink-0" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        <span className="font-semibold">{t("room.pausedTitle")}</span>{" "}
        <span className="text-foreground/80">
          {canManage ? t("room.pausedBody") : t("room.pausedBodyReadOnly")}
        </span>
      </p>
      {canManage ? (
        <Button
          size="sm"
          disabled={resume.isPending}
          onClick={() =>
            resume.mutate(undefined, { onError: () => toast.error(t("room.resumeFailed")) })
          }
        >
          {t("room.resume")}
        </Button>
      ) : null}
    </div>
  );
}

function RoomSkeleton() {
  return (
    <div data-fill className="flex min-h-0 flex-1 flex-col" aria-hidden="true">
      <div className="space-y-2 border-b px-4 py-3 md:px-6">
        <Skeleton className="h-5 w-40" />
        <Skeleton className="h-3.5 w-64 max-w-full" />
      </div>
    </div>
  );
}

function RoomNotFound() {
  const t = useT();
  return (
    <EmptyState
      icon={MessageSquareOffIcon}
      title={t("room.notFoundTitle")}
      action={
        <Button asChild variant="outline">
          <Link to="/rooms">{t("room.backToRooms")}</Link>
        </Button>
      }
    >
      <p>{t("room.notFoundBody")}</p>
    </EmptyState>
  );
}
