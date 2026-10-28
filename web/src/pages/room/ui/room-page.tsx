import { useMutationState } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import {
  FilesIcon,
  MessageSquareOffIcon,
  PlusIcon,
  Settings2Icon,
  UsersRoundIcon,
} from "lucide-react";
import { useEffect, useState } from "react";
import { MemberPicker } from "@/components/member-picker";
import { useMe } from "@/entities/account";
import { postMessageKey } from "@/entities/message";
import {
  canPost,
  conversationTitle,
  historyPlatform,
  isHistoryOnly,
  useAutoMarkRoomRead,
  useOpenConversation,
  useRoom,
} from "@/entities/room";
import { useRoomMembers } from "@/entities/room-member";
import { useDraft, useDraftStore } from "@/features/send-message";
import { isApiError } from "@/shared/api/client";
import {
  ConversationKindDM,
  type IntelligenceRef,
  LevelManage,
  type Message,
  type RoomMember,
} from "@/shared/api/types.gen";
import { useI18n, useT } from "@/shared/i18n";
import { useIsMobile } from "@/shared/lib/device";
import { BackToConversations } from "@/shared/ui/molecules/back-link";
import { NotFound } from "@/shared/ui/molecules/not-found";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/shared/ui/molecules/sheet";
import { Button } from "@/shared/ui/primitives/button";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/shared/ui/primitives/tabs";
import { Composer } from "@/widgets/composer";
import { ConversationHeader } from "@/widgets/conversation-header";
import {
  HistoryNotice,
  type MessageFocus,
  MessageTimeline,
  PausedBanner,
  useTimelineMessages,
} from "@/widgets/conversation-timeline";
import { MemberPanel } from "@/widgets/member-panel";
import { RoomFiles } from "@/widgets/room-files";
import { RoomSettingsSheet } from "@/widgets/room-settings";

const routeApi = getRouteApi("/_app/rooms/$roomId");

/** The conversation page: the header, the timeline, the composer and the side panel (members, files). */
export function RoomPage() {
  const { t } = useI18n();
  const { roomId } = routeApi.useParams();
  const room = useRoom(roomId);
  const members = useRoomMembers(roomId);
  const me = useMe().data;
  const isMobile = useIsMobile();
  useOpenConversation(roomId);
  useAutoMarkRoomRead(roomId);
  // A post refused as history (CID-21): the DM held a person the room data did not reveal.
  const refusedAsHistory = useMutationState({
    filters: { mutationKey: postMessageKey(roomId) },
    select: (mutation) => mutation.state.error,
  }).some((error) => isApiError(error) && error.code === "history_only");
  const [panelOpen, setPanelOpen] = useState(true);
  const [sheetOpen, setSheetOpen] = useState(false);
  // The members sheet of a phone belongs to the conversation it was opened in: reaching another
  // one (a DM opened from a profile) must not leave it laid over the new page.
  const [sheetRoom, setSheetRoom] = useState(roomId);
  if (sheetRoom !== roomId) {
    setSheetRoom(roomId);
    setSheetOpen(false);
  }
  const [panelTab, setPanelTab] = useState<PanelTab>("members");
  const [focus, setFocus] = useState<MessageFocus | null>(null);
  // The messages as the timeline has them now: a reply and an edit follow their message by id, so
  // an edit shows its new text and a deletion takes the action back (EDT-06, DLT-06).
  const messages = useTimelineMessages(roomId);
  // The message the next one answers (ACT-07). The choice is part of the room's draft, so it
  // survives going to another conversation and back (WA-07); a message that is gone or was
  // deleted is dropped, one the timeline has not loaded yet just waits for it.
  const replyId = useDraft(roomId).replyTo;
  const setReply = useDraftStore.getState().setReply;
  const replyMessage = replyId ? messages.get(replyId) : undefined;
  const replyTo = liveMessage(replyMessage ?? null, messages);
  const replyDeleted = replyMessage?.deleted_at !== undefined;
  useEffect(() => {
    if (replyDeleted) setReply(roomId, null);
  }, [replyDeleted, roomId, setReply]);
  // The message being edited in the composer (EDT-10).
  const [editChoice, setEditChoice] = useState<Message | null>(null);
  const editing = liveMessage(editChoice, messages);
  if (editChoice && editing === null) setEditChoice(null);

  if (room.isError && isApiError(room.error) && room.error.status === 404) {
    return <RoomNotFound />;
  }
  if (!room.data || !me) return <RoomSkeleton back={isMobile} />;

  const r = room.data;
  // Nobody manages a DM: its participants are fixed and it cannot be shared (DM-02).
  const isDM = r.kind === ConversationKindDM;
  const canManage = !isDM && r.my_level === LevelManage;
  const history = historyPlatform(r);
  const historyOnly = isHistoryOnly(r, refusedAsHistory);
  // Edit needs a composer to edit in; a conversation that is history changes no more (EDT-05, DLT-03).
  // A message is answered or edited, one at a time: choosing either lets the other go.
  const onEdit =
    canPost(r.my_level) && !historyOnly
      ? (message: Message) => {
          setReply(roomId, null);
          setEditChoice(message);
        }
      : undefined;
  // Reply needs a composer to answer in (ACT-07).
  const onReply =
    canPost(r.my_level) && !historyOnly
      ? (message: Message) => {
          setEditChoice(null);
          setReply(roomId, message.id);
        }
      : undefined;
  const memberList = members.data ?? [];
  const participantCount = isDM ? r.participants.length : memberList.length;
  const title = conversationTitle(r, me.id, t);

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
  const membersLabel = t(isDM ? "participants.title" : "members.title");
  const panelLabel = panelTab === "files" ? t("files.title") : membersLabel;
  const panel = (
    <SidePanel
      tab={panelTab}
      onTabChange={setPanelTab}
      roomId={roomId}
      members={memberList}
      participants={isDM ? r.participants : undefined}
      membersLabel={membersLabel}
      count={participantCount}
      canManage={canManage}
      loading={!isDM && members.isPending}
      onOpenMessage={openInTimeline}
    />
  );

  return (
    <div data-fill className="flex min-h-0 flex-1">
      <div className="flex min-w-0 flex-1 flex-col">
        <ConversationHeader
          room={r}
          viewerId={me.id}
          back={isMobile}
          actions={
            <>
              <Button
                variant={!isMobile && shown("members") ? "secondary" : "ghost"}
                size="sm"
                onClick={() => togglePanel("members")}
                aria-expanded={shown("members")}
                aria-label={t(isDM ? "room.participantsToggle" : "room.membersToggle", {
                  count: participantCount,
                })}
              >
                <UsersRoundIcon aria-hidden="true" />
                <span className="font-mono text-xs tabular">{participantCount}</span>
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
                <RoomSettingsSheet
                  room={r}
                  trigger={
                    <Button variant="ghost" size="icon-sm" aria-label={t("settings.title")}>
                      <Settings2Icon aria-hidden="true" />
                    </Button>
                  }
                />
              ) : null}
            </>
          }
        />
        {r.agents_paused ? <PausedBanner room={r} /> : null}
        <MessageTimeline
          key={roomId}
          roomId={roomId}
          meId={me.id}
          members={memberList}
          focus={focus}
          onReply={onReply}
          canModerate={canManage}
          onEdit={onEdit}
          canReact={canPost(r.my_level) && !historyOnly}
          frozen={historyOnly}
        />
        {historyOnly ? (
          <HistoryNotice platform={history ?? ""} />
        ) : (
          <Composer
            key={`composer-${roomId}`}
            roomId={roomId}
            roomName={title}
            level={r.my_level}
            members={memberList}
            replyTo={replyTo}
            onClearReply={() => setReply(roomId, null)}
            editing={editing}
            onEditDone={() => setEditChoice(null)}
          />
        )}
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
              <SheetDescription>{title}</SheetDescription>
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

/**
 * The message as the timeline has it now, or null when it is gone or was deleted: a placeholder is
 * no one's to answer or edit (DLT-10).
 */
function liveMessage(choice: Message | null, messages: Map<string, Message>): Message | null {
  if (!choice) return null;
  const live = messages.get(choice.id);
  return live !== undefined && live.deleted_at === undefined ? live : null;
}

/**
 * The conversation's side panel: its members and files as tabs (FILES-04). A DM lists its two
 * participants and offers no way to change them (DM-02).
 */
function SidePanel({
  tab,
  onTabChange,
  roomId,
  members,
  participants,
  membersLabel,
  count,
  canManage,
  loading,
  onOpenMessage,
}: {
  tab: PanelTab;
  onTabChange: (tab: PanelTab) => void;
  roomId: string;
  members: RoomMember[];
  /** Set for a DM: its participants replace the member list. */
  participants?: IntelligenceRef[];
  membersLabel: string;
  count: number;
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
      <h2 className="sr-only">{tab === "files" ? t("files.title") : membersLabel}</h2>
      <div className="flex h-12 shrink-0 items-center justify-between gap-2 border-b px-2">
        <TabsList variant="line" className="h-full">
          <TabsTrigger value="members" className="px-2">
            {membersLabel}
            <span className="font-mono text-xs text-muted-foreground tabular">{count}</span>
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
        <MemberPanel
          roomId={roomId}
          members={members}
          participants={participants}
          canManage={canManage}
          loading={loading}
        />
      </TabsContent>
      <TabsContent value="files" className="flex min-h-0 flex-col rounded-none">
        <RoomFiles roomId={roomId} onOpenMessage={onOpenMessage} />
      </TabsContent>
    </Tabs>
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
    <NotFound
      icon={MessageSquareOffIcon}
      title={t("room.notFoundTitle")}
      body={t("room.notFoundBody")}
      action={
        <Button asChild variant="outline">
          <Link to="/">{t("nav.back")}</Link>
        </Button>
      }
    />
  );
}
