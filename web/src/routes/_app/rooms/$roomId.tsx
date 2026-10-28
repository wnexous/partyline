import { createFileRoute } from "@tanstack/react-router";
import { RoomPage } from "@/pages/room";

export const Route = createFileRoute("/_app/rooms/$roomId")({
  component: RoomPage,
});
