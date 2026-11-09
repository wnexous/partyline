import type { FileTransfer, PostMessageInput } from "@/entities/message";
import { deleteUpload, deleteUploadsOnExit } from "@/shared/api/uploads";
import type { AttachedFile } from "@/shared/ui/molecules/attachment-preview";
import { useDraftStore } from "./draft-store";
import { useTransferStore } from "./transfer-store";

/*
 * The glue between a send and the stores (LF-01): the files of the post are known to the upload
 * code by their File object and to the composer by their draft id. Every callback writes to the
 * stores, never to a component, so a send goes on and ends right when the composer is gone.
 *
 * A session the person can still go on with (the file is in the draft, its send failed) is kept;
 * one nobody will use any more is deleted on the server, best effort: a file cancelled or taken
 * out of the draft, a draft dropped with its conversation, the drafts of a page that goes away.
 * Otherwise it would hold its place in the person's uploads until it expires, up to a day later.
 */

/** Whether a file is still in a room's draft. */
const inDraft = (roomId: string, fileId: number) =>
  (useDraftStore.getState().drafts[roomId]?.files ?? []).some((f) => f.id === fileId);

/** Forgets a file's upload and deletes its session on the server, if it had one. */
function discardUpload(fileId: number) {
  const session = useTransferStore.getState().files[fileId]?.session;
  useTransferStore.getState().forget([fileId]);
  if (session) void deleteUpload(session);
}

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
      if (id === undefined) return;
      // The file was cancelled while its session was being created: nobody will resume it.
      if (!inDraft(roomId, id)) {
        void deleteUpload(upload.id);
        return;
      }
      store().session(id, upload.id);
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

/**
 * Cancels the room's send that is uploading and takes this file out of the draft. The chunk in
 * flight is aborted first; the session's DELETE then waits out the server finishing with it.
 */
export function cancelUpload(roomId: string, fileId: number) {
  useTransferStore.getState().cancel(roomId);
  removeDraftFile(roomId, fileId);
}

/** Takes a file out of the draft, and its upload session off the server. */
export function removeDraftFile(roomId: string, fileId: number) {
  useDraftStore.getState().removeFile(roomId, fileId);
  discardUpload(fileId);
}

/**
 * Drops a conversation's whole draft (the conversation was deleted): its send stops and the
 * sessions of its files are deleted.
 */
export function discardDraft(roomId: string) {
  useTransferStore.getState().cancel(roomId);
  const files = useDraftStore.getState().drafts[roomId]?.files ?? [];
  useDraftStore.getState().clear(roomId);
  for (const f of files) discardUpload(f.id);
}

/**
 * Deletes every session of the drafts when the page is unloaded for good (not when it goes to the
 * back-forward cache, from which it may come back). Returns the function that stops listening.
 */
export function discardUploadsOnPageExit(): () => void {
  const onPageHide = (e: PageTransitionEvent) => {
    if (e.persisted) return;
    const sessions = Object.values(useTransferStore.getState().files)
      .map((f) => f.session)
      .filter((s): s is string => Boolean(s));
    deleteUploadsOnExit(sessions);
  };
  window.addEventListener("pagehide", onPageHide);
  return () => window.removeEventListener("pagehide", onPageHide);
}
