import { useMutationState } from "@tanstack/react-query";
import { createFileRoute, Link } from "@tanstack/react-router";
import {
  FilesIcon,
  HistoryIcon,
  MessageSquareOffIcon,
  PauseIcon,
  PlusIcon,
  Settings2Icon,
  UsersRoundIcon,
} from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { BackToConversations } from "@/components/back-link";
import { Composer, canPost } from "@/components/composer";
import {
  ConversationHeader,
  conversationTitle,
  historyPlatform,
} from "@/components/conversation-header";
import { EmptyState } from "@/components/empty-state";
import { MemberList } from "@/components/member-list";
import { MemberPicker } from "@/components/member-picker";
import { platformName } from "@/components/message-item";
import {
  type MessageFocus,
  MessageTimeline,
  useTimelineMessages,
} from "@/components/message-timeline";
import { ParticipantList } from "@/components/participant-list";
import { RoomFiles } from "@/components/room-files";
import { RoomSettingsSheet } from "@/components/room-settings-sheet";
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
import { useMarkRoomRead } from "@/hooks/use-mark-room-read";
import { useIsMobile } from "@/hooks/use-mobile";
import { useOpenConversation } from "@/hooks/use-open-conversation";
import { isApiError } from "@/lib/api/client";
import { postMessageKey, useMe, useResumeRoom, useRoom, useRoomMembers } from "@/lib/api/queries";
import {
  ConversationKindDM,
  type IntelligenceRef,
  LevelManage,
  type Message,
  type Room,
  type RoomMember,
} from "@/lib/api/types.gen";
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
  useMarkRoomRead(roomId);
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
  // The message the next one answers (ACT-07).
  const [replyChoice, setReplyChoice] = useState<Message | null>(null);
  const replyTo = liveMessage(replyChoice, messages);
  if (replyChoice && replyTo === null) setReplyChoice(null);
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
  const historyOnly = history !== null || refusedAsHistory;
  // Edit needs a composer to edit in; a conversation that is history changes no more (EDT-05, DLT-03).
  // A message is answered or edited, one at a time: choosing either lets the other go.
  const onEdit =
    canPost(r.my_level) && !historyOnly
      ? (message: Message) => {
          setReplyChoice(null);
          setEditChoice(message);
        }
      : undefined;
  // Reply needs a composer to answer in (ACT-07).
  const onReply =
    canPost(r.my_level) && !historyOnly
      ? (message: Message) => {
          setEditChoice(null);
          setReplyChoice(message);
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
      loading={members.isPending}
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
            onClearReply={() => setReplyChoice(null)}
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
        {participants ? (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <ParticipantList participants={participants} />
          </div>
        ) : (
          <MembersPanel roomId={roomId} members={members} canManage={canManage} loading={loading} />
        )}
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

/**
 * A platform DM with a person is history (CID-21): the person answers on their platform, through
 * the connection that holds them, and partyline keeps the conversation only to be read.
 */
function HistoryNotice({ platform }: { platform: string }) {
  const t = useT();
  return (
    <div
      role="note"
      data-testid="history-notice"
      className="flex items-start gap-2.5 border-t bg-muted/40 px-4 py-3 md:px-6"
    >
      <HistoryIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      <div className="min-w-0 space-y-0.5">
        <p className="text-sm font-medium">
          {platform
            ? t("room.history", { platform: platformName(platform) })
            : t("room.historyUnknown")}
        </p>
        <p className="text-xs text-muted-foreground">{t("room.historyHint")}</p>
      </div>
    </div>
  );
}

/**
 * Tells that the loop guard paused the agents of a conversation (DLV-08). Resuming is up to whoever
 * the server says may (DLV-09): a group's managers and, since nobody manages a DM, the people who
 * can see it - its human participants and whoever manages one of its AIs.
 */
function PausedBanner({ room }: { room: Room }) {
  const t = useT();
  const resume = useResumeRoom(room.id);
  const canResume = room.can_resume;
  return (
    <div
      role="status"
      className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-tape-strong/40 bg-muted px-4 py-2.5 text-sm md:px-6"
    >
      <PauseIcon className="size-4 shrink-0" aria-hidden="true" />
      <p className="min-w-0 flex-1">
        <span className="font-semibold">{t("room.pausedTitle")}</span>{" "}
        <span className="text-foreground/80">
          {canResume ? t("room.pausedBody") : t("room.pausedBodyReadOnly")}
        </span>
      </p>
      {canResume ? (
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
