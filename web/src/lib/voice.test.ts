import { afterEach, describe, expect, it, vi } from "vitest";
import {
  canRecord,
  formatDuration,
  isAudio,
  isVoiceName,
  isVoicePreview,
  micFailure,
  pickRecorderMime,
  voiceFileName,
} from "@/lib/voice";

/*
 * Voice messages, the parts that need no browser (VOI-01, VOI-04, VOI-06, VOI-07): which format to
 * record in, what to call the file, how long it was, and what a refused microphone means.
 */

afterEach(() => vi.unstubAllGlobals());

describe("pickRecorderMime (VOI-04)", () => {
  it("records WebM/Opus when the browser can", () => {
    expect(pickRecorderMime(() => true)).toBe("audio/webm;codecs=opus");
  });

  it("falls back to Ogg/Opus, which is what Firefox records", () => {
    const supported = (type: string) => type === "audio/ogg;codecs=opus";
    expect(pickRecorderMime(supported)).toBe("audio/ogg;codecs=opus");
  });

  it("asks for nothing when neither is available, so the browser records what it records (Safari: MP4)", () => {
    expect(pickRecorderMime(() => false)).toBeUndefined();
  });

  it("asks the browser about each format it would use, not about unrelated ones", () => {
    const asked: string[] = [];
    pickRecorderMime((type) => {
      asked.push(type);
      return false;
    });
    expect(asked).toEqual(["audio/webm;codecs=opus", "audio/ogg;codecs=opus"]);
  });
});

describe("voiceFileName (VOI-04)", () => {
  const at = new Date(2026, 9, 24, 14, 5, 9);

  it("names the file after the moment it was recorded, in the format's own extension", () => {
    expect(voiceFileName("audio/webm;codecs=opus", at)).toBe(
      "voice-message-2026-10-24-140509.webm",
    );
    expect(voiceFileName("audio/ogg;codecs=opus", at)).toBe("voice-message-2026-10-24-140509.ogg");
    expect(voiceFileName("audio/mp4", at)).toBe("voice-message-2026-10-24-140509.m4a");
    expect(voiceFileName("audio/mp4;codecs=mp4a.40.2", at)).toBe(
      "voice-message-2026-10-24-140509.m4a",
    );
    expect(voiceFileName("audio/mpeg", at)).toBe("voice-message-2026-10-24-140509.mp3");
  });

  it("pads every part of the date and time", () => {
    expect(voiceFileName("audio/webm", new Date(2026, 0, 2, 3, 4, 5))).toBe(
      "voice-message-2026-01-02-030405.webm",
    );
  });

  it("falls back to .audio for a format it does not know, never an empty or odd extension", () => {
    expect(voiceFileName("audio/x-strange-thing", at)).toBe(
      "voice-message-2026-10-24-140509.audio",
    );
    expect(voiceFileName("", at)).toBe("voice-message-2026-10-24-140509.audio");
  });
});

describe("isVoiceName", () => {
  it("knows the file a recording makes, and nothing else", () => {
    expect(isVoiceName("voice-message-2026-10-24-140509.webm")).toBe(true);
    expect(isVoiceName("voice-message-2026-10-24-140509.m4a")).toBe(true);
    expect(isVoiceName("voice-message.webm")).toBe(false);
    expect(isVoiceName("my voice-message-2026-10-24-140509.webm")).toBe(false);
    expect(isVoiceName("holiday.mp3")).toBe(false);
  });
});

describe("isVoicePreview", () => {
  it("is the list's text for a message that is only a recording", () => {
    expect(isVoicePreview("[voice-message-2026-10-24-140509.webm]")).toBe(true);
  });

  it("is not text a person wrote, nor the preview of another file", () => {
    expect(isVoicePreview("voice-message-2026-10-24-140509.webm")).toBe(false);
    expect(isVoicePreview("[holiday.mp3]")).toBe(false);
    expect(isVoicePreview("see [voice-message-2026-10-24-140509.webm]")).toBe(false);
    expect(isVoicePreview("")).toBe(false);
  });
});

describe("formatDuration (VOI-02, VOI-07)", () => {
  it("reads minutes and seconds, the seconds always two digits", () => {
    expect(formatDuration(0)).toBe("0:00");
    expect(formatDuration(7)).toBe("0:07");
    expect(formatDuration(65)).toBe("1:05");
    expect(formatDuration(600)).toBe("10:00");
  });

  it("drops the fraction rather than rounding up: 0:59.9 is still 0:59", () => {
    expect(formatDuration(59.9)).toBe("0:59");
    expect(formatDuration(0.4)).toBe("0:00");
  });

  it("keeps counting minutes past an hour", () => {
    expect(formatDuration(3725)).toBe("62:05");
  });

  it("shows 0:00 for what is not a length", () => {
    expect(formatDuration(-3)).toBe("0:00");
    expect(formatDuration(Number.NaN)).toBe("0:00");
    expect(formatDuration(Number.POSITIVE_INFINITY)).toBe("0:00");
  });
});

describe("micFailure (VOI-06)", () => {
  const named = (name: string) => Object.assign(new Error(name), { name });

  it.each([
    ["NotAllowedError", "denied"],
    ["SecurityError", "denied"],
    ["PermissionDeniedError", "denied"],
    ["NotFoundError", "missing"],
    ["DevicesNotFoundError", "missing"],
    ["OverconstrainedError", "missing"],
    ["NotReadableError", "busy"],
    ["TrackStartError", "busy"],
    ["AbortError", "busy"],
    ["TypeError", "failed"],
    ["SomethingElse", "failed"],
  ] as const)("%s means %s", (name, failure) => {
    expect(micFailure(named(name))).toBe(failure);
  });

  it("calls anything that is not an error a failure", () => {
    expect(micFailure(undefined)).toBe("failed");
    expect(micFailure("denied")).toBe("failed");
  });
});

describe("canRecord (VOI-01)", () => {
  class Recorder {}
  const devices = { getUserMedia: () => Promise.resolve({}) };

  it("is true with MediaRecorder and a microphone API", () => {
    vi.stubGlobal("MediaRecorder", Recorder);
    vi.stubGlobal("navigator", { mediaDevices: devices });
    expect(canRecord()).toBe(true);
  });

  it("is false without MediaRecorder", () => {
    vi.stubGlobal("MediaRecorder", undefined);
    vi.stubGlobal("navigator", { mediaDevices: devices });
    expect(canRecord()).toBe(false);
  });

  it("is false without a microphone API (an insecure page has no mediaDevices)", () => {
    vi.stubGlobal("MediaRecorder", Recorder);
    vi.stubGlobal("navigator", {});
    expect(canRecord()).toBe(false);
    vi.stubGlobal("navigator", { mediaDevices: {} });
    expect(canRecord()).toBe(false);
  });
});

describe("isAudio (VOI-07)", () => {
  it("is an audio type, whatever parameters the recorder added to it", () => {
    expect(isAudio("audio/webm;codecs=opus")).toBe(true);
    expect(isAudio("audio/ogg; codecs=opus")).toBe(true);
    expect(isAudio("AUDIO/MPEG")).toBe(true);
    expect(isAudio("video/webm")).toBe(false);
    expect(isAudio("application/octet-stream")).toBe(false);
    expect(isAudio("")).toBe(false);
  });
});
