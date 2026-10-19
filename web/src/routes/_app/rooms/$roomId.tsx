import { createFileRoute, Link } from "@tanstack/react-router";
import { MessageSquareOffIcon } from "lucide-react";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { VisibilityLabel } from "@/components/room-visibility";
import { Button } from "@/components/ui/button";
import { isApiError } from "@/lib/api/client";
import { useRoom } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";

export const Route = createFileRoute("/_app/rooms/$roomId")({
  component: RoomPage,
});

function RoomPage() {
  const { roomId } = Route.useParams();
  const room = useRoom(roomId);

  if (room.isError && isApiError(room.error) && room.error.status === 404) {
    return <RoomNotFound />;
  }
  if (!room.data) return null;

  return (
    <PageHeader
      title={room.data.name}
      description={room.data.description || undefined}
      actions={<VisibilityLabel visibility={room.data.visibility} />}
    />
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
