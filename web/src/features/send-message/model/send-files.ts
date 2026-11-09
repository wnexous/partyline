import type { FileTransfer, PostMessageInput } from "@/entities/message";
import type { AttachedFile } from "@/shared/ui/molecules/attachment-preview";
import { useDraftStore } from "./draft-store";
import { useTransferStore } from "./transfer-store";

/*
 * The glue between a send and the stores (LF-01): the files of the post are known to the upload
 * code by their File object and to the composer by their draft id. Every callback writes to the
 * stores, never to a component, so a send goes on and ends right when the composer is gone.
 */

/** The transfer of one send of `attached` in a room: its progress, sessions and cancel go to the store. */
export function transferFor(roomId: string, attached: readonly AttachedFile[]): FileTransfer {
  const controller = new AbortController();
  const idOf = new Map(attached.map((a) => [a.file, a.id]));
  const store = useTransferStore.getState;
  return {
    signal: controller.signal,
    onStart: () => store().begin(roomId, { controller, fileIds: attached.map((a) => a.id) }),
    onEnd: () => store().end(roomId, controller),
    onProgress: (file, progress) => {
      const id = idOf.get(file);
      if (id !== undefined) store().progress(id, progress);
    },
    onSession: (file, upload) => {
      const id = idOf.get(file);
      if (id !== undefined) store().session(id, upload.id);
    },
    sessionOf: (file) => {
      const id = idOf.get(file);
      return id === undefined ? undefined : store().files[id]?.session;
    },
  };
}

/** A post went through: what it carried leaves the room's draft, and the uploads of its files are forgotten. */
export function clearSentFiles(roomId: string, input: PostMessageInput) {
  const drafts = useDraftStore.getState();
  const sent = new Set(input.files ?? []);
  const ids = (drafts.drafts[roomId]?.files ?? []).filter((f) => sent.has(f.file)).map((f) => f.id);
  useTransferStore.getState().forget(ids);
  drafts.clearSent(roomId, { text: input.text ?? "", files: input.files, replyTo: input.reply_to });
}

/** Cancels the room's send that is uploading and takes this file out of the draft. */
export function cancelUpload(roomId: string, fileId: number) {
  useTransferStore.getState().cancel(roomId);
  removeDraftFile(roomId, fileId);
}

/** Takes a file out of the draft, with what was known of its upload. */
export function removeDraftFile(roomId: string, fileId: number) {
  useDraftStore.getState().removeFile(roomId, fileId);
  useTransferStore.getState().forget([fileId]);
}
