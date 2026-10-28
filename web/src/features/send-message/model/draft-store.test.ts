import { beforeEach, describe, expect, it } from "vitest";
import { EMPTY_DRAFT, useDraftStore } from "./draft-store";

const store = () => useDraftStore.getState();
const draftOf = (roomId: string) => store().drafts[roomId] ?? EMPTY_DRAFT;
const file = (name: string) => new File(["x"], name);

beforeEach(() => useDraftStore.setState({ drafts: {}, editDrafts: {} }));

describe("draft store (WA-07)", () => {
  it("keeps the text, the files and the reply of each conversation apart", () => {
    store().setText("r1", "one");
    store().addFiles("r1", [file("a.log")]);
    store().setReply("r1", "m-1");
    store().setText("r2", "two");

    expect(draftOf("r1")).toMatchObject({ text: "one", replyTo: "m-1" });
    expect(draftOf("r1").files.map((f) => f.file.name)).toEqual(["a.log"]);
    expect(draftOf("r2")).toMatchObject({ text: "two", replyTo: null, files: [] });
  });

  it("gives files ids that stay unique across conversations, and removes one by id", () => {
    store().addFiles("r1", [file("a"), file("b")]);
    store().addFiles("r2", [file("c")]);
    const [a, b] = draftOf("r1").files;
    expect(new Set([a?.id, b?.id, draftOf("r2").files[0]?.id]).size).toBe(3);
    store().removeFile("r1", a?.id ?? -1);
    expect(draftOf("r1").files.map((f) => f.file.name)).toEqual(["b"]);
  });

  it("clear drops only the given conversation", () => {
    store().setText("r1", "one");
    store().setReply("r1", "m-1");
    store().setText("r2", "two");
    store().clear("r1");
    expect(draftOf("r1")).toBe(EMPTY_DRAFT);
    expect(draftOf("r2").text).toBe("two");
  });

  it("returns the same empty draft for a conversation without one, and forgets an emptied draft", () => {
    expect(draftOf("nobody")).toBe(EMPTY_DRAFT);
    store().setText("r1", "x");
    store().setText("r1", "");
    expect(store().drafts).toEqual({});
  });

  it("edit drafts are per message and never touch the draft of a conversation", () => {
    store().setText("r1", "new message");
    store().setEditDraft("m-1", "edited one");
    store().setEditDraft("m-2", "edited two");
    store().clearEditDraft("m-1");

    expect(store().editDrafts).toEqual({ "m-2": "edited two" });
    expect(draftOf("r1").text).toBe("new message");
  });
});
