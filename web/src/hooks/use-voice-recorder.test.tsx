import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useVoiceRecorder } from "@/hooks/use-voice-recorder";
import { MAX_RECORDING_MS } from "@/lib/voice";
import { type FakeMicrophone, FakeRecorder, stubMicrophone } from "@/test/recorder";

/*
 * The recorder behind the microphone button (VOI-01..VOI-06): it asks for the microphone, records
 * in the format the browser can, counts the time, and lets go of the microphone the moment the
 * recording ends, whichever way it ends.
 */

let mic: FakeMicrophone;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 9, 24, 14, 5, 9));
  mic = stubMicrophone();
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const render = () => renderHook(() => useVoiceRecorder());
const start = (hook: ReturnType<typeof render>) => act(() => hook.result.current.start());
const lastRecorder = () =>
  FakeRecorder.instances[FakeRecorder.instances.length - 1] as FakeRecorder;

describe("starting (VOI-01, VOI-02)", () => {
  it("is supported where there is a MediaRecorder and a microphone API", () => {
    expect(render().result.current.supported).toBe(true);
  });

  it("is not supported without MediaRecorder, or without a microphone API", () => {
    vi.stubGlobal("MediaRecorder", undefined);
    expect(render().result.current.supported).toBe(false);
    vi.stubGlobal("MediaRecorder", FakeRecorder);
    vi.stubGlobal("navigator", { mediaDevices: undefined });
    expect(render().result.current.supported).toBe(false);
  });

  it("asks for the microphone only, and records WebM/Opus at a voice bit rate", async () => {
    const hook = render();
    await start(hook);
    expect(mic.getUserMedia).toHaveBeenCalledWith({ audio: true });
    expect(lastRecorder().options).toEqual({
      mimeType: "audio/webm;codecs=opus",
      audioBitsPerSecond: 32_000,
    });
    expect(lastRecorder().state).toBe("recording");
    expect(hook.result.current.phase).toBe("recording");
  });

  it("records Ogg/Opus where WebM is not an option", async () => {
    FakeRecorder.supported = new Set(["audio/ogg;codecs=opus"]);
    const hook = render();
    await start(hook);
    expect(lastRecorder().options.mimeType).toBe("audio/ogg;codecs=opus");
  });

  it("leaves the format to the browser when it can record neither", async () => {
    FakeRecorder.supported = new Set();
    const hook = render();
    await start(hook);
    expect(lastRecorder().options).toEqual({ audioBitsPerSecond: 32_000 });
  });

  it("waits for the answer to the permission prompt before it counts as recording", async () => {
    let allow: (stream: MediaStream) => void = () => {};
    mic.getUserMedia.mockImplementationOnce(
      () =>
        new Promise<MediaStream>((resolve) => {
          allow = resolve;
        }),
    );
    const hook = render();
    let started: Promise<void> = Promise.resolve();
    act(() => {
      started = hook.result.current.start();
    });
    expect(hook.result.current.phase).toBe("requesting");
    await act(async () => {
      allow(mic.stream);
      await started;
    });
    expect(hook.result.current.phase).toBe("recording");
  });

  it("does not open a second recording while one is on", async () => {
    const hook = render();
    await start(hook);
    await start(hook);
    expect(mic.getUserMedia).toHaveBeenCalledTimes(1);
    expect(FakeRecorder.instances).toHaveLength(1);
  });

  it("counts the time up while it records", async () => {
    const hook = render();
    await start(hook);
    expect(hook.result.current.elapsedMs).toBe(0);
    act(() => vi.advanceTimersByTime(2_000));
    expect(hook.result.current.elapsedMs).toBe(2_000);
    act(() => vi.advanceTimersByTime(3_250));
    expect(hook.result.current.elapsedMs).toBeGreaterThanOrEqual(5_000);
    expect(hook.result.current.elapsedMs).toBeLessThanOrEqual(5_250);
  });
});

describe("stopping (VOI-04)", () => {
  it("hands over a file named after the moment, in the format that was recorded", async () => {
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(3_000));
    let rec: Awaited<ReturnType<typeof hook.result.current.stop>> = null;
    await act(async () => {
      rec = await hook.result.current.stop();
    });
    expect(rec).not.toBeNull();
    const { file, durationMs } = rec as unknown as { file: File; durationMs: number };
    expect(file.name).toBe("voice-message-2026-10-24-140512.webm");
    expect(file.type).toBe("audio/webm;codecs=opus");
    expect(await file.text()).toBe("voice");
    expect(durationMs).toBe(3_000);
    expect(hook.result.current.phase).toBe("recorded");
    expect(hook.result.current.recording?.file).toBe(file);
  });

  it("names the file after what the browser recorded when it chose the format", async () => {
    FakeRecorder.supported = new Set();
    const hook = render();
    await start(hook);
    await act(async () => {
      await hook.result.current.stop();
    });
    expect(hook.result.current.recording?.file.type).toBe("audio/mp4");
    expect(hook.result.current.recording?.file.name).toMatch(/^voice-message-.*\.m4a$/);
  });

  it("lets go of the microphone as soon as it stops", async () => {
    const hook = render();
    await start(hook);
    expect(mic.track.stop).not.toHaveBeenCalled();
    await act(async () => {
      await hook.result.current.stop();
    });
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });

  it("stops the clock where it stopped", async () => {
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(4_000));
    await act(async () => {
      await hook.result.current.stop();
    });
    act(() => vi.advanceTimersByTime(10_000));
    expect(hook.result.current.recording?.durationMs).toBe(4_000);
  });

  it("answers with the recording that is already waiting when stopped again", async () => {
    const hook = render();
    await start(hook);
    let first: unknown;
    let second: unknown;
    await act(async () => {
      first = await hook.result.current.stop();
      second = await hook.result.current.stop();
    });
    expect(second).toBe(first);
    expect(FakeRecorder.instances).toHaveLength(1);
  });

  it("stops by itself at the limit and keeps the recording to be sent (VOI-05)", async () => {
    const hook = render();
    await start(hook);
    await act(async () => {
      vi.advanceTimersByTime(MAX_RECORDING_MS);
      await Promise.resolve();
    });
    expect(hook.result.current.phase).toBe("recorded");
    expect(hook.result.current.recording?.durationMs).toBe(MAX_RECORDING_MS);
    expect(mic.track.stop).toHaveBeenCalled();
  });

  it("keeps what was recorded when the recording ends by itself, as when the microphone goes", async () => {
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(2_000));
    await act(async () => {
      lastRecorder().end();
      await Promise.resolve();
    });
    expect(hook.result.current.phase).toBe("recorded");
    expect(hook.result.current.recording?.durationMs).toBe(2_000);
    expect(mic.track.stop).toHaveBeenCalled();
  });
});

describe("cancelling (VOI-05)", () => {
  it("throws the recording away and lets go of the microphone at once", async () => {
    const hook = render();
    await start(hook);
    act(() => hook.result.current.cancel());
    expect(hook.result.current.phase).toBe("idle");
    expect(hook.result.current.recording).toBeNull();
    expect(hook.result.current.elapsedMs).toBe(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
    // The recorder's own stop event arrives later and must not bring the recording back.
    await act(async () => {
      await Promise.resolve();
    });
    expect(hook.result.current.phase).toBe("idle");
    expect(hook.result.current.recording).toBeNull();
  });

  it("answers a stop that was waiting for the recording with nothing", async () => {
    const hook = render();
    await start(hook);
    let answer: unknown = "pending";
    act(() => {
      void hook.result.current.stop().then((rec) => {
        answer = rec;
      });
      hook.result.current.cancel();
    });
    await act(async () => {
      await Promise.resolve();
    });
    expect(answer).toBeNull();
  });

  it("discards a recording that waits to be sent", async () => {
    const hook = render();
    await start(hook);
    await act(async () => {
      await hook.result.current.stop();
    });
    act(() => hook.result.current.cancel());
    expect(hook.result.current.phase).toBe("idle");
    expect(hook.result.current.recording).toBeNull();
  });

  it("releases a microphone that was granted after the user had already given up", async () => {
    let allow: (stream: MediaStream) => void = () => {};
    mic.getUserMedia.mockImplementationOnce(
      () =>
        new Promise<MediaStream>((resolve) => {
          allow = resolve;
        }),
    );
    const hook = render();
    let started: Promise<void> = Promise.resolve();
    act(() => {
      started = hook.result.current.start();
    });
    act(() => hook.result.current.cancel());
    await act(async () => {
      allow(mic.stream);
      await started;
    });
    expect(hook.result.current.phase).toBe("idle");
    expect(FakeRecorder.instances).toHaveLength(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });

  it("can start again after a cancel", async () => {
    const hook = render();
    await start(hook);
    act(() => hook.result.current.cancel());
    await start(hook);
    expect(hook.result.current.phase).toBe("recording");
    expect(FakeRecorder.instances).toHaveLength(2);
  });
});

describe("when the microphone is not available (VOI-06)", () => {
  it.each([
    ["NotAllowedError", "denied"],
    ["NotFoundError", "missing"],
    ["NotReadableError", "busy"],
    ["TypeError", "failed"],
  ] as const)("%s: says %s and records nothing", async (name, failure) => {
    mic = stubMicrophone({ name });
    const hook = render();
    await start(hook);
    expect(hook.result.current.failure).toBe(failure);
    expect(hook.result.current.phase).toBe("idle");
    expect(FakeRecorder.instances).toHaveLength(0);
  });

  it("forgets the failure when the user tries again", async () => {
    mic = stubMicrophone({ name: "NotAllowedError" });
    const hook = render();
    await start(hook);
    expect(hook.result.current.failure).toBe("denied");
    mic.getUserMedia.mockImplementationOnce(async () => mic.stream);
    await start(hook);
    expect(hook.result.current.failure).toBeNull();
    expect(hook.result.current.phase).toBe("recording");
  });

  it("lets the page forget a failure it has shown", async () => {
    mic = stubMicrophone({ name: "NotFoundError" });
    const hook = render();
    await start(hook);
    expect(hook.result.current.failure).toBe("missing");
    act(() => hook.result.current.clearFailure());
    expect(hook.result.current.failure).toBeNull();
  });

  it("says it failed, and lets go, when the recorder cannot be made", async () => {
    FakeRecorder.supported = new Set();
    vi.stubGlobal(
      "MediaRecorder",
      Object.assign(
        class {
          constructor() {
            throw new Error("no encoder");
          }
        },
        { isTypeSupported: () => false },
      ),
    );
    const hook = render();
    await start(hook);
    expect(hook.result.current.failure).toBe("failed");
    expect(hook.result.current.phase).toBe("idle");
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });

  it("drops a recording the recorder reports an error for", async () => {
    const hook = render();
    await start(hook);
    await act(async () => {
      lastRecorder().onerror?.(new Event("error"));
      await Promise.resolve();
    });
    expect(hook.result.current.failure).toBe("failed");
    expect(hook.result.current.phase).toBe("idle");
    expect(mic.track.stop).toHaveBeenCalled();
  });
});

describe("leaving the page (VOI-05)", () => {
  it("lets go of the microphone when the composer closes in the middle of a recording", async () => {
    const hook = render();
    await start(hook);
    hook.unmount();
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
    expect(lastRecorder().state).toBe("inactive");
    // Nothing is left running to update a component that is gone.
    await Promise.resolve();
    act(() => vi.advanceTimersByTime(MAX_RECORDING_MS));
  });

  it("lets go of a microphone granted after the composer closed", async () => {
    let allow: (stream: MediaStream) => void = () => {};
    mic.getUserMedia.mockImplementationOnce(
      () =>
        new Promise<MediaStream>((resolve) => {
          allow = resolve;
        }),
    );
    const hook = render();
    let started: Promise<void> = Promise.resolve();
    act(() => {
      started = hook.result.current.start();
    });
    hook.unmount();
    await act(async () => {
      allow(mic.stream);
      await started;
    });
    expect(FakeRecorder.instances).toHaveLength(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });
});
