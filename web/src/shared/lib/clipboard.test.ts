import { afterEach, describe, expect, it, vi } from "vitest";
import { copyText } from "@/shared/lib/clipboard";

/*
 * Copy text (ACT-05): the Clipboard API where the page may use it, a hidden box and `copy` where
 * it may not, and an honest answer when neither works.
 */

afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("copyText", () => {
  it("writes to the clipboard and says it did", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    expect(await copyText("deploy finished")).toBe(true);
    expect(writeText).toHaveBeenCalledWith("deploy finished");
  });

  it("keeps the text exactly as it is: lines, spaces and all", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    await copyText("  line one\n\tline two  ");
    expect(writeText).toHaveBeenCalledWith("  line one\n\tline two  ");
  });

  it("copies from a hidden box where there is no clipboard API, and leaves nothing behind", async () => {
    vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
    let copied = "";
    document.execCommand = vi.fn((command: string) => {
      copied = (document.querySelector("textarea") as HTMLTextAreaElement).value;
      return command === "copy";
    });
    expect(await copyText("from plain http")).toBe(true);
    expect(copied).toBe("from plain http");
    expect(document.querySelector("textarea")).toBeNull();
  });

  it("falls back to the box when the clipboard refuses", async () => {
    const writeText = vi.fn(async () => {
      throw new DOMException("not allowed", "NotAllowedError");
    });
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    document.execCommand = vi.fn(() => true);
    expect(await copyText("second try")).toBe(true);
    expect(document.execCommand).toHaveBeenCalledWith("copy");
  });

  it("says it could not when nothing works", async () => {
    vi.stubGlobal("navigator", { ...navigator, clipboard: undefined });
    document.execCommand = vi.fn(() => false);
    expect(await copyText("lost")).toBe(false);
    document.execCommand = vi.fn(() => {
      throw new Error("unsupported");
    });
    expect(await copyText("lost")).toBe(false);
    expect(document.querySelector("textarea")).toBeNull();
  });
});
