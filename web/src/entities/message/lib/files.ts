import {
  type FileKind,
  FileKindDocument,
  type Message,
  type RoomFile,
} from "@/shared/api/types.gen";

/** The kind the server gives a file: its MIME family, document for everything else. */
export function fileKindOf(mime: string): FileKind {
  const family = mime.toLowerCase().split("/")[0];
  return family === "image" || family === "audio" || family === "video" ? family : FileKindDocument;
}

/** A message's attachments as room files, newest first like the files list. */
export function messageFiles(m: Message): RoomFile[] {
  return m.attachments
    .map((a) => ({
      ...a,
      kind: fileKindOf(a.mime),
      message_id: m.id,
      message_seq: m.seq,
      author_kind: m.author_kind,
      author_name: m.author_name,
      author: m.author,
      created_at: m.created_at,
    }))
    .reverse();
}
