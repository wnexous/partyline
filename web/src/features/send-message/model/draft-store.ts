import { create } from "zustand";
import type { AttachedFile } from "@/shared/ui/molecules/attachment-preview";

/*
 * Composer drafts (WA-07): what was typed, attached and chosen to answer in each conversation,
 * kept in memory for the browser tab so that switching to another conversation, page or list and
 * back finds it again. Not persisted: a File cannot go to storage, and a reload starts clean.
 * An edit has a draft of its own per message id, so editing never overwrites the new-message
 * draft. Caret and mention state follow the caret, not the draft, and stay in the composer.
 */

export interface Draft {
  text: string;
  files: AttachedFile[];
  /** The id of the message the next one answers. */
  replyTo: string | null;
}

/** What a conversation without a draft reads: one stable object, so selectors do not re-render. */
export const EMPTY_DRAFT: Draft = Object.freeze({ text: "", files: [], replyTo: null }) as Draft;

interface DraftState {
  drafts: Record<string, Draft>;
  editDrafts: Record<string, string>;
  setText: (roomId: string, text: string) => void;
  addFiles: (roomId: string, files: readonly File[]) => void;
  removeFile: (roomId: string, fileId: number) => void;
  setReply: (roomId: string, messageId: string | null) => void;
  /** Drops the whole draft of one conversation (after a successful send). */
  clear: (roomId: string) => void;
  setEditDraft: (messageId: string, text: string) => void;
  clearEditDraft: (messageId: string) => void;
}

/** Ids are unique for the tab, so a file keeps its identity across conversations. */
let nextFileId = 0;

const isEmpty = (d: Draft) => d.text === "" && d.files.length === 0 && d.replyTo === null;

/** Writes one conversation's draft; an empty draft is removed so the store does not grow. */
function put(drafts: Record<string, Draft>, roomId: string, change: Partial<Draft>) {
  const next = { ...(drafts[roomId] ?? EMPTY_DRAFT), ...change };
  const { [roomId]: _gone, ...rest } = drafts;
  return isEmpty(next) ? rest : { ...rest, [roomId]: next };
}

export const useDraftStore = create<DraftState>()((set) => ({
  drafts: {},
  editDrafts: {},
  setText: (roomId, text) => set((s) => ({ drafts: put(s.drafts, roomId, { text }) })),
  addFiles: (roomId, picked) =>
    set((s) => {
      if (picked.length === 0) return s;
      const added = picked.map((file) => ({ id: ++nextFileId, file }));
      const files = [...(s.drafts[roomId]?.files ?? []), ...added];
      return { drafts: put(s.drafts, roomId, { files }) };
    }),
  removeFile: (roomId, fileId) =>
    set((s) => {
      const files = (s.drafts[roomId]?.files ?? []).filter((f) => f.id !== fileId);
      return { drafts: put(s.drafts, roomId, { files }) };
    }),
  setReply: (roomId, messageId) =>
    set((s) => ({ drafts: put(s.drafts, roomId, { replyTo: messageId }) })),
  clear: (roomId) =>
    set((s) => {
      const { [roomId]: _gone, ...rest } = s.drafts;
      return { drafts: rest };
    }),
  setEditDraft: (messageId, text) =>
    set((s) => ({ editDrafts: { ...s.editDrafts, [messageId]: text } })),
  clearEditDraft: (messageId) =>
    set((s) => {
      const { [messageId]: _gone, ...rest } = s.editDrafts;
      return { editDrafts: rest };
    }),
}));

/** The draft of one conversation, or the stable empty one. */
export const useDraft = (roomId: string): Draft =>
  useDraftStore((s) => s.drafts[roomId] ?? EMPTY_DRAFT);

/** The text typed while editing a message, or undefined when the edit has not been touched. */
export const useEditDraft = (messageId: string | null): string | undefined =>
  useDraftStore((s) => (messageId === null ? undefined : s.editDrafts[messageId]));

/** Forgets every draft; for tests. */
export const resetDrafts = () => useDraftStore.setState({ drafts: {}, editDrafts: {} });
