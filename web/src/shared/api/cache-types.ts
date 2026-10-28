import type { InfiniteData } from "@tanstack/react-query";
import type { FileKind, IntelligenceRef, Message, Page, RoomFile } from "@/shared/api/types.gen";

export type MessagePages = InfiniteData<Page<Message>, number | undefined>;

export type PeoplePages = InfiniteData<Page<IntelligenceRef>, string | undefined>;

export type FilePages = InfiniteData<Page<RoomFile>, string | undefined>;

/** A files list filter: one kind, or every file. */
export type FileFilter = FileKind | "all";
