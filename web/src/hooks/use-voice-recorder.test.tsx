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

/* A WebM built here by hand, and read back with a minimal reader: what a player would see. */

const textBytes = (value: string) => [...new TextEncoder().encode(value)];

/** An EBML variable-length size of `value` in `width` bytes. */
function vint(value: number, width = 1): number[] {
  const bytes = Array<number>(width).fill(0);
  let rest = value;
  for (let i = width - 1; i >= 0; i--) {
    bytes[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  bytes[0] = (bytes[0] as number) | (0x80 >> (width - 1));
  return bytes;
}

/** One EBML element: its ID, the size of its data and the data. */
const element = (id: number[], data: number[], sizeWidth = 1): number[] => [
  ...id,
  ...vint(data.length, sizeWidth),
  ...data,
];

const float32Bytes = (value: number): number[] => {
  const bytes = new Uint8Array(4);
  new DataView(bytes.buffer).setFloat32(0, value, false);
  return [...bytes];
};

const uintBytes = (value: number, width: number): number[] => {
  const bytes = Array<number>(width).fill(0);
  let rest = value;
  for (let i = width - 1; i >= 0; i--) {
    bytes[i] = rest % 256;
    rest = Math.floor(rest / 256);
  }
  return bytes;
};

/** A recording as MediaRecorder writes it: a Segment of unknown size, Info with no Duration, a Cluster. */
function syntheticWebm(scale = 1_000_000): Uint8Array<ArrayBuffer> {
  const info = element(
    [0x15, 0x49, 0xa9, 0x66],
    [
      ...element([0x2a, 0xd7, 0xb1], uintBytes(scale, 3)),
      ...element([0x4d, 0x80], textBytes("partyline-test")),
    ],
  );
  return new Uint8Array([
    ...element([0x1a, 0x45, 0xdf, 0xa3], element([0x42, 0x82], textBytes("webm"))),
    // The Segment: its size is unknown, so it runs to the end of the file.
    0x18,
    0x53,
    0x80,
    0x67,
    0x01,
    0xff,
    0xff,
    0xff,
    0xff,
    0xff,
    0xff,
    0xff,
    ...info,
    ...element([0x1f, 0x43, 0xb6, 0x75], [0, 0, 0, 0]),
  ]);
}

/** The Android Chrome layout: a known-size Segment and an Info with a four-byte Duration. */
function syntheticAndroidWebm(): Uint8Array<ArrayBuffer> {
  const block = (rel: number): number[] =>
    element([0xa3], [0x81, ...uintBytes(rel < 0 ? rel + 0x10000 : rel, 2), 0x80, 0, 0]);
  const cluster = (timecode: number, relatives: number[]): number[] =>
    element(
      [0x1f, 0x43, 0xb6, 0x75],
      [...element([0xe7], uintBytes(timecode, 4)), ...relatives.flatMap(block)],
    );
  const info = element(
    [0x15, 0x49, 0xa9, 0x66],
    [
      ...element([0x2a, 0xd7, 0xb1], uintBytes(1_000_000, 3)),
      ...element([0x44, 0x89], float32Bytes(1_936_186)),
      ...element([0x4d, 0x80], textBytes("Chrome")),
    ],
  );
  return new Uint8Array([
    ...element([0x1a, 0x45, 0xdf, 0xa3], element([0x42, 0x82], textBytes("webm"))),
    ...element(
      [0x18, 0x53, 0x80, 0x67],
      [...info, ...cluster(0, [0]), ...cluster(1_662_000, [0, 20])],
    ),
  ]);
}

interface Span {
  id: number;
  dataStart: number;
  dataLength: number;
  end: number;
}

/** What a reader of the container sees of one element at `offset`. */
function readAt(bytes: Uint8Array, offset: number): Span {
  const first = bytes[offset] as number;
  let idWidth = 1;
  while (idWidth <= 4 && (first & (0x80 >> (idWidth - 1))) === 0) idWidth++;
  let id = 0;
  for (let i = 0; i < idWidth; i++) id = id * 256 + (bytes[offset + i] as number);
  const sizeFirst = bytes[offset + idWidth] as number;
  let sizeWidth = 1;
  while (sizeWidth <= 8 && (sizeFirst & (0x80 >> (sizeWidth - 1))) === 0) sizeWidth++;
  let dataLength = sizeFirst & ((0x80 >> (sizeWidth - 1)) - 1);
  for (let i = 1; i < sizeWidth; i++) {
    dataLength = dataLength * 256 + (bytes[offset + idWidth + i] as number);
  }
  const dataStart = offset + idWidth + sizeWidth;
  return { id, dataStart, dataLength, end: dataStart + dataLength };
}

function childrenOf(bytes: Uint8Array, parent: Span, end: number): Span[] {
  const found: Span[] = [];
  let cursor = parent.dataStart;
  while (cursor < end) {
    const child = readAt(bytes, cursor);
    found.push(child);
    cursor = child.end;
  }
  return found;
}

/** The Duration element in the file, as the container carries it. */
function durationSpan(bytes: Uint8Array): Span | undefined {
  const header = readAt(bytes, 0);
  const segment = readAt(bytes, header.end);
  const info = childrenOf(bytes, segment, bytes.length).find((child) => child.id === 0x1549a966);
  if (!info) throw new Error("no Info");
  return childrenOf(bytes, info, info.end).find((child) => child.id === 0x4489);
}

/** The Duration as a player reads it, in the width the file already carries it. */
function durationIn(bytes: Uint8Array): number {
  const duration = durationSpan(bytes);
  if (!duration) throw new Error("no Duration");
  const view = new DataView(
    bytes.buffer,
    bytes.byteOffset + duration.dataStart,
    duration.dataLength,
  );
  return duration.dataLength === 4 ? view.getFloat32(0, false) : view.getFloat64(0, false);
}

/** Each Cluster's Timecode, as a player reads the file's timeline. */
function clusterTimecodesIn(bytes: Uint8Array): number[] {
  const header = readAt(bytes, 0);
  const segment = readAt(bytes, header.end);
  return childrenOf(bytes, segment, bytes.length)
    .filter((child) => child.id === 0x1f43b675)
    .map((cluster) => {
      const timecode = childrenOf(bytes, cluster, cluster.end).find((child) => child.id === 0xe7);
      if (!timecode) throw new Error("no Timecode");
      let value = 0;
      for (let i = 0; i < timecode.dataLength; i++) {
        value = value * 256 + (bytes[timecode.dataStart + i] as number);
      }
      return value;
    });
}

describe("the length in the file (VOI-08)", () => {
  it("writes the recorded time into the WebM it hands over", async () => {
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(4_000));
    await act(async () => {
      lastRecorder().ondataavailable?.({
        data: new Blob([syntheticWebm()], { type: "audio/webm;codecs=opus" }),
      });
      // The stand-in hands over its own chunk on stop; this test recorded the bytes above.
      lastRecorder().ondataavailable = null;
      await hook.result.current.stop();
    });
    const recording = hook.result.current.recording;
    if (!recording) throw new Error("no recording");
    expect(recording.durationMs).toBe(4_000);
    expect(recording.file.type).toBe("audio/webm;codecs=opus");
    expect(recording.file.name).toBe("voice-message-2026-10-24-140513.webm");
    const bytes = new Uint8Array(await recording.file.arrayBuffer());
    expect(durationIn(bytes)).toBe(4_000);
  });

  it("closes a false gap and keeps the four-byte Duration of an Android recording", async () => {
    const original = syntheticAndroidWebm();
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(4_000));
    await act(async () => {
      lastRecorder().ondataavailable?.({
        data: new Blob([original], { type: "audio/webm;codecs=opus" }),
      });
      lastRecorder().ondataavailable = null;
      await hook.result.current.stop();
    });
    const recording = hook.result.current.recording;
    if (!recording) throw new Error("no recording");
    const bytes = new Uint8Array(await recording.file.arrayBuffer());
    // In place: the file keeps its length and its four-byte Duration, and the false gap is gone.
    expect(bytes.length).toBe(original.length);
    expect(durationSpan(bytes)?.dataLength).toBe(4);
    expect(durationIn(bytes)).toBe(4_000);
    expect(clusterTimecodesIn(bytes)).toEqual([0, 20]);
  });

  it("leaves a format that carries its length alone", async () => {
    FakeRecorder.supported = new Set();
    const hook = render();
    await start(hook);
    act(() => vi.advanceTimersByTime(4_000));
    await act(async () => {
      await hook.result.current.stop();
    });
    const recording = hook.result.current.recording;
    if (!recording) throw new Error("no recording");
    expect(new Uint8Array(await recording.file.arrayBuffer())).toEqual(
      new TextEncoder().encode("voice"),
    );
  });

  it("answers nothing when the recording was cancelled", async () => {
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
    expect(hook.result.current.recording).toBeNull();
  });
});

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

  it("says it failed, and lets go, when the recorder cannot start", async () => {
    class ThrowingRecorder extends FakeRecorder {
      override start(): never {
        throw new DOMException("cannot start", "InvalidStateError");
      }
    }
    vi.stubGlobal("MediaRecorder", ThrowingRecorder);
    // More than one track: the microphone is only let go when every one of them is stopped.
    const second = { stop: vi.fn() };
    const stream = { getTracks: () => [mic.track, second] } as unknown as MediaStream;
    mic.getUserMedia.mockImplementation(async () => stream);
    const hook = render();
    await start(hook);
    expect(hook.result.current.failure).toBe("failed");
    expect(hook.result.current.phase).toBe("idle");
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
    expect(second.stop).toHaveBeenCalledTimes(1);
    // Nothing the failed start left behind is still running: the clock stays where it was.
    act(() => vi.advanceTimersByTime(MAX_RECORDING_MS));
    expect(hook.result.current.elapsedMs).toBe(0);
    expect(hook.result.current.phase).toBe("idle");
    expect(hook.result.current.recording).toBeNull();
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
