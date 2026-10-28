import { describe, expect, it } from "vitest";
import { messageExcerpt } from "@/entities/message";
import { makeMessage } from "@/test/fixtures";

const VOICE = "Voice message";
const file = (name: string) => ({
  id: "a1",
  name,
  mime: "audio/webm",
  size: 10,
  status: "stored" as const,
});

describe("messageExcerpt", () => {
  it("is the text, on one line", () => {
    expect(messageExcerpt(makeMessage({ text: "  deploy\n\n  finished \t ok " }), VOICE)).toBe(
      "deploy finished ok",
    );
  });

  it("cuts a long text and says so, without splitting an emoji", () => {
    const long = `${"a".repeat(139)}😀${"b".repeat(50)}`;
    const cut = messageExcerpt(makeMessage({ text: long }), VOICE);
    expect(cut).toBe(`${"a".repeat(139)}😀…`);
    expect(Array.from(cut)).toHaveLength(141);
  });

  it("leaves a text that just fits alone", () => {
    const fits = "x".repeat(140);
    expect(messageExcerpt(makeMessage({ text: fits }), VOICE)).toBe(fits);
  });

  it("names a recording as one, whatever the file is called", () => {
    const message = makeMessage({
      text: "",
      attachments: [file("voice-message-2026-10-24-140509.webm")],
    });
    expect(messageExcerpt(message, VOICE)).toBe("Voice message");
  });

  it("names any other file by its own name", () => {
    expect(messageExcerpt(makeMessage({ text: "", attachments: [file("notes.pdf")] }), VOICE)).toBe(
      "notes.pdf",
    );
  });

  it("prefers the text to the files", () => {
    const message = makeMessage({ text: "see attached", attachments: [file("notes.pdf")] });
    expect(messageExcerpt(message, VOICE)).toBe("see attached");
  });

  it("is empty for a message with nothing in it", () => {
    expect(messageExcerpt(makeMessage({ text: "   " }), VOICE)).toBe("");
  });
});
