import { create } from "zustand";
import type { UploadProgress } from "@/shared/api/uploads";

/*
 * The files of a post that go up through upload sessions (LF-01), by the id each one has in the
 * draft: how far it got and the session it went into. The send of a room holds the controller that
 * stops it while its files are uploading; a cancel aborts it. The store is in memory for the tab,
 * like the drafts, so leaving the conversation and coming back finds the progress where it is, and
 * a send that failed goes on with the sessions it started instead of sending everything again.
 */

/** One draft file's upload: bytes the server holds, and the session they are in. */
export interface FileUpload extends UploadProgress {
  session?: string;
}

/** A room's send whose files are going up. */
export interface ActiveTransfer {
  controller: AbortController;
  /** The draft ids of the files it sends, in order. */
  fileIds: readonly number[];
}

interface TransferState {
  files: Record<number, FileUpload>;
  active: Record<string, ActiveTransfer>;
  /** The files of a room's send started going up through sessions. */
  begin: (roomId: string, transfer: ActiveTransfer) => void;
  /** That send ended (sent, failed or cancelled); the progress of each file stays for a retry. */
  end: (roomId: string, controller: AbortController) => void;
  progress: (fileId: number, progress: UploadProgress) => void;
  session: (fileId: number, session: string) => void;
  /** Stops the room's send that is uploading, if there is one. */
  cancel: (roomId: string) => void;
  /** Drops what is known of these files (they were sent or removed). */
  forget: (fileIds: readonly number[]) => void;
}

export const useTransferStore = create<TransferState>()((set, get) => ({
  files: {},
  active: {},
  begin: (roomId, transfer) => set((s) => ({ active: { ...s.active, [roomId]: transfer } })),
  end: (roomId, controller) =>
    set((s) => {
      if (s.active[roomId]?.controller !== controller) return s;
      const { [roomId]: _done, ...rest } = s.active;
      return { active: rest };
    }),
  progress: (fileId, { sent, total }) =>
    set((s) => ({ files: { ...s.files, [fileId]: { ...s.files[fileId], sent, total } } })),
  session: (fileId, session) =>
    set((s) => ({
      files: { ...s.files, [fileId]: { sent: 0, total: 0, ...s.files[fileId], session } },
    })),
  cancel: (roomId) => get().active[roomId]?.controller.abort(),
  forget: (fileIds) =>
    set((s) => {
      if (!fileIds.some((id) => id in s.files)) return s;
      const files = { ...s.files };
      for (const id of fileIds) delete files[id];
      return { files };
    }),
}));

/** The send of a room whose files are uploading, or undefined. */
export const useActiveTransfer = (roomId: string): ActiveTransfer | undefined =>
  useTransferStore((s) => s.active[roomId]);

/** What the uploads of a room's draft files got to, by draft id. */
export const useFileUploads = (): Record<number, FileUpload> => useTransferStore((s) => s.files);

/** Forgets every transfer; for tests. */
export const resetTransfers = () => useTransferStore.setState({ files: {}, active: {} });
