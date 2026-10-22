import { createFileRoute, Link } from "@tanstack/react-router";
import {
  FilesIcon,
  MessageSquareOffIcon,
  PauseIcon,
  PlusIcon,
  Settings2Icon,
  Share2Icon,
  UsersRoundIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { BackToConversations } from "@/components/back-link";
import { Composer } from "@/components/composer";
import { EmptyState } from "@/components/empty-state";
import { MemberList } from "@/components/member-list";
import { MemberPicker } from "@/components/member-picker";
import { type MessageFocus, MessageTimeline } from "@/components/message-timeline";
import { RoomFiles } from "@/components/room-files";
import { RoomSettingsSheet } from "@/components/room-settings-sheet";
import { VisibilityLabel } from "@/components/room-visibility";
import { SharingDialog } from "@/components/sharing-dialog";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useIsMobile } from "@/hooks/use-mobile";
import { useOpenConversation } from "@/hooks/use-open-conversation";
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
  useOpenConversation(roomId);
  const [panelOpen, setPanelOpen] = useState(true);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [panelTab, setPanelTab] = useState<PanelTab>("members");
  const [focus, setFocus] = useState<MessageFocus | null>(null);

  if (room.isError && isApiError(room.error) && room.error.status === 404) {
    return <RoomNotFound />;
  }
  if (!room.data || !me) return <RoomSkeleton back={isMobile} />;

  const r = room.data;
  const canManage = r.my_level === LevelManage;
  const memberList = members.data ?? [];

  const shown = (tab: PanelTab) => (isMobile ? sheetOpen : panelOpen) && panelTab === tab;
  const togglePanel = (tab: PanelTab) => {
    if (isMobile) {
      setPanelTab(tab);
      setSheetOpen(true);
    } else if (shown(tab)) {
      setPanelOpen(false);
    } else {
      setPanelTab(tab);
      setPanelOpen(true);
    }
  };
  const openInTimeline = (target: MessageFocus) => {
    setFocus({ ...target });
    if (isMobile) setSheetOpen(false);
  };
  const panelLabel = t(panelTab === "files" ? "files.title" : "members.title");
  const panel = (
    <SidePanel
      tab={panelTab}
      onTabChange={setPanelTab}
      roomId={roomId}
      members={memberList}
      canManage={canManage}
      loading={members.isPending}
      onOpenMessage={openInTimeline}
    />
  );

  return (
    <div data-fill className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex items-center gap-2 border-b px-4 py-2.5 md:px-6">
          {isMobile ? <BackToConversations className="-ml-3" /> : null}
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-lg font-semibold tracking-[-0.015em]">{r.name}</h1>
            <p className="flex min-w-0 items-center gap-2 text-sm text-muted-foreground">
              <VisibilityLabel visibility={r.visibility} className="shrink-0" />
              {r.description ? <span className="truncate">{r.description}</span> : null}
            </p>
          </div>
          <Button
            variant={!isMobile && shown("members") ? "secondary" : "ghost"}
            size="sm"
            onClick={() => togglePanel("members")}
            aria-expanded={shown("members")}
            aria-label={t("room.membersToggle", { count: memberList.length })}
          >
            <UsersRoundIcon aria-hidden="true" />
            <span className="font-mono text-xs tabular">{memberList.length}</span>
          </Button>
          <Button
            variant={!isMobile && shown("files") ? "secondary" : "ghost"}
            size="icon-sm"
            onClick={() => togglePanel("files")}
            aria-expanded={shown("files")}
            aria-label={t("room.filesToggle")}
          >
            <FilesIcon aria-hidden="true" />
          </Button>
          {canManage ? (
            <SharingDialog
              kind="rooms"
              id={r.id}
              name={r.name}
              owner={r.owner}
              trigger={
                <Button variant="ghost" size="sm" aria-label={t("room.share")}>
                  <Share2Icon aria-hidden="true" />
                  <span className="hidden sm:inline">{t("room.share")}</span>
                </Button>
              }
            />
          ) : null}
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
        <MessageTimeline
          key={roomId}
          roomId={roomId}
          meId={me.id}
          members={memberList}
          focus={focus}
        />
        <Composer
          key={`composer-${roomId}`}
          roomId={roomId}
          roomName={r.name}
          level={r.my_level}
          members={memberList}
          broadcast={r.broadcast}
        />
      </div>

      {!isMobile && panelOpen ? (
        <aside
          aria-label={panelLabel}
          className="flex w-72 shrink-0 flex-col border-l bg-sidebar/40 xl:w-80"
        >
          {panel}
        </aside>
      ) : null}
      {isMobile ? (
        <Sheet open={sheetOpen} onOpenChange={setSheetOpen}>
          <SheetContent className="w-[min(22rem,100vw)] gap-0 p-0">
            <SheetHeader className="sr-only">
              <SheetTitle>{panelLabel}</SheetTitle>
              <SheetDescription>{r.name}</SheetDescription>
            </SheetHeader>
            <aside aria-label={panelLabel} className="flex min-h-0 flex-1 flex-col">
              {panel}
            </aside>
          </SheetContent>
        </Sheet>
      ) : null}
    </div>
  );
}

type PanelTab = "members" | "files";

/** The room side panel: members and files as tabs (FILES-04). */
function SidePanel({
  tab,
  onTabChange,
  roomId,
  members,
  canManage,
  loading,
  onOpenMessage,
}: {
  tab: PanelTab;
  onTabChange: (tab: PanelTab) => void;
  roomId: string;
  members: RoomMember[];
  canManage: boolean;
  loading: boolean;
  onOpenMessage: (target: MessageFocus) => void;
}) {
  const t = useT();
  return (
    <Tabs
      value={tab}
      onValueChange={(v) => onTabChange(v as PanelTab)}
      className="min-h-0 flex-1 gap-0"
    >
      <h2 className="sr-only">{t(tab === "files" ? "files.title" : "members.title")}</h2>
      <div className="flex h-12 shrink-0 items-center justify-between gap-2 border-b px-2">
        <TabsList variant="line" className="h-full">
          <TabsTrigger value="members" className="px-2">
            {t("members.title")}
            <span className="font-mono text-xs text-muted-foreground tabular">
              {members.length}
            </span>
          </TabsTrigger>
          <TabsTrigger value="files" className="px-2">
            {t("files.title")}
          </TabsTrigger>
        </TabsList>
        {tab === "members" && canManage ? (
          <MemberPicker
            roomId={roomId}
            members={members}
            trigger={
              <Button variant="outline" size="xs" className="mr-8 md:mr-2">
                <PlusIcon aria-hidden="true" />
                {t("picker.title")}
              </Button>
            }
          />
        ) : null}
      </div>
      <TabsContent value="members" className="flex min-h-0 flex-col rounded-none">
        <MembersPanel roomId={roomId} members={members} canManage={canManage} loading={loading} />
      </TabsContent>
      <TabsContent value="files" className="flex min-h-0 flex-col rounded-none">
        <RoomFiles roomId={roomId} onOpenMessage={onOpenMessage} />
      </TabsContent>
    </Tabs>
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
  return (
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

function RoomSkeleton({ back }: { back: boolean }) {
  return (
    <div data-fill className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center gap-2 border-b px-4 py-3 md:px-6">
        {back ? <BackToConversations className="-ml-3" /> : null}
        <div aria-hidden="true" className="space-y-2">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="h-3.5 w-64 max-w-full" />
        </div>
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
          <Link to="/">{t("nav.back")}</Link>
        </Button>
      }
    >
      <p>{t("room.notFoundBody")}</p>
    </EmptyState>
  );
}
