import { createFileRoute, Link } from "@tanstack/react-router";
import { MessageSquareOffIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { MessageTimeline } from "@/components/message-timeline";
import { VisibilityLabel } from "@/components/room-visibility";
import { Button } from "@/components/ui/button";
import { isApiError } from "@/lib/api/client";
import { useMe, useRoom, useRoomMembers } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/rooms/$roomId")({
  component: RoomPage,
});

function RoomPage() {
  const { roomId } = Route.useParams();
  const room = useRoom(roomId);
  const members = useRoomMembers(roomId);
  const me = useMe().data;

  if (room.isError && isApiError(room.error) && room.error.status === 404) {
    return <RoomNotFound />;
  }
  if (!room.data || !me) return null;

  return (
    <div data-fill className="flex min-h-0 flex-1 flex-col">
      <header className="flex items-center gap-3 border-b px-4 py-3 md:px-6">
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-lg font-semibold tracking-[-0.015em]">{room.data.name}</h1>
          {room.data.description ? (
            <p className="truncate text-sm text-muted-foreground">{room.data.description}</p>
          ) : null}
        </div>
        <VisibilityLabel visibility={room.data.visibility} />
      </header>
      <MessageTimeline key={roomId} roomId={roomId} meId={me.id} members={members.data} />
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
