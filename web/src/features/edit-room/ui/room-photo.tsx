import { ConversationFace, useRemoveRoomAvatar, useUploadRoomAvatar } from "@/entities/room";
import { photoErrorText } from "@/shared/api/error-text";
import type { Room } from "@/shared/api/types.gen";
import { useT } from "@/shared/i18n";
import { PhotoEditor } from "@/shared/ui/molecules/photo-editor";

/**
 * The group's photo: the editor every picture shares, wired to this room's routes. It sends a chosen
 * file at once, so the photo does not wait for the Save of the form it sits in (GAV-06).
 */
export function RoomPhoto({ room }: { room: Room }) {
  const t = useT();
  const upload = useUploadRoomAvatar(room.id);
  const remove = useRemoveRoomAvatar(room.id);
  return (
    <PhotoEditor
      face={
        <ConversationFace
          group={{ id: room.id, name: room.name, avatarUrl: room.avatar_url }}
          size="xl"
          decorative
        />
      }
      hasPhoto={Boolean(room.avatar_url)}
      upload={upload.mutateAsync}
      errorText={(err) => photoErrorText(t, err)}
      remove={(done) => remove.mutate(undefined, done)}
      removing={remove.isPending}
      className="sm:w-auto"
    />
  );
}
