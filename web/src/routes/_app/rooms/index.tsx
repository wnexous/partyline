import { createFileRoute, Link } from "@tanstack/react-router";
import { MessagesSquareIcon, PauseIcon, PlusIcon } from "lucide-react";
import { useMemo } from "react";
import { CreateRoomDialog } from "@/components/create-room-dialog";
import { EmptyState } from "@/components/empty-state";
import { PageHeader } from "@/components/page-header";
import { VisibilityLabel } from "@/components/room-visibility";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useMe, useRooms } from "@/lib/api/queries";
import type { Room } from "@/lib/api/types.gen";
import { useI18n } from "@/lib/i18n";
import { formatAbsolute, formatRelative } from "@/lib/time";

export const Route = createFileRoute("/_app/rooms/")({
  component: RoomsPage,
});

function byActivity(a: Room, b: Room) {
  return Date.parse(b.updated_at) - Date.parse(a.updated_at);
}

function RoomsPage() {
  const { t } = useI18n();
  const rooms = useRooms();
  const sorted = useMemo(() => (rooms.data ? [...rooms.data].sort(byActivity) : []), [rooms.data]);

  const newRoom = (
    <CreateRoomDialog
      trigger={
        <Button>
          <PlusIcon aria-hidden="true" />
          {t("rooms.new")}
        </Button>
      }
    />
  );

  return (
    <>
      <PageHeader
        title={t("page.roomsTitle")}
        description={t("page.roomsBody")}
        actions={sorted.length > 0 ? newRoom : null}
      />
      {rooms.isPending ? (
        <RoomListSkeleton />
      ) : rooms.isError ? (
        <div className="px-4 py-10 text-sm md:px-8" role="alert">
          <p className="text-destructive">{t("rooms.loadError")}</p>
          <Button variant="outline" size="sm" className="mt-3" onClick={() => rooms.refetch()}>
            {t("common.retry")}
          </Button>
        </div>
      ) : sorted.length === 0 ? (
        <EmptyState icon={MessagesSquareIcon} title={t("rooms.emptyTitle")} action={newRoom}>
          <p>{t("rooms.emptyBody")}</p>
        </EmptyState>
      ) : (
        <ul aria-label={t("rooms.listLabel")} className="divide-y border-b">
          {sorted.map((room) => (
            <li key={room.id}>
              <RoomRow room={room} />
            </li>
          ))}
        </ul>
      )}
    </>
  );
}

// SPEC_DEVIATION: rows show visibility, owner and last activity but no member count.
// Reason: dto.Room has no member count and fetching every room's members would be N requests;
// add the count to the row when the DTO carries it.
function RoomRow({ room }: { room: Room }) {
  const { t, locale } = useI18n();
  const me = useMe().data;
  const foreign = me && room.owner.id !== me.id;
  return (
    <Link
      to="/rooms/$roomId"
      params={{ roomId: room.id }}
      className="group grid grid-cols-[1fr_auto] items-center gap-x-4 gap-y-1 px-4 py-3.5 outline-none transition-colors hover:bg-accent/60 focus-visible:bg-accent focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:ring-inset md:px-8"
    >
      <span className="flex min-w-0 items-center gap-2">
        <span className="truncate font-semibold tracking-[-0.01em]">{room.name}</span>
        {room.agents_paused ? (
          <span className="inline-flex shrink-0 items-center gap-1 rounded-[3px] bg-warning/20 px-1.5 py-0.5 text-[0.7rem] font-semibold text-foreground ring-1 ring-warning/50 ring-inset">
            <PauseIcon className="size-3" aria-hidden="true" />
            {t("rooms.paused")}
          </span>
        ) : null}
      </span>
      <span
        className="row-span-2 self-center text-right font-mono text-xs text-muted-foreground tabular"
        title={formatAbsolute(room.updated_at, locale)}
      >
        {room.last_seq > 0 ? formatRelative(room.updated_at, locale) : null}
      </span>
      <span className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-sm text-muted-foreground">
        <VisibilityLabel visibility={room.visibility} />
        {foreign ? (
          <span className="text-xs">{t("rooms.ownedBy", { username: room.owner.username })}</span>
        ) : null}
        <span className="min-w-0 truncate">
          {room.description || (room.last_seq === 0 ? t("rooms.noActivity") : null)}
        </span>
      </span>
    </Link>
  );
}

function RoomListSkeleton() {
  return (
    <ul aria-hidden="true" className="divide-y border-b">
      {[0, 1, 2].map((i) => (
        <li key={i} className="space-y-2 px-4 py-4 md:px-8">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-64 max-w-full" />
        </li>
      ))}
    </ul>
  );
}
