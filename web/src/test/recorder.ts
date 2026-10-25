import { vi } from "vitest";

/*
 * A stand-in for the microphone and MediaRecorder, for tests (VOI-01..VOI-06). Nothing records:
 * `stop()` hands over the chunk "voice" and fires `stop` a microtask later, as a browser does.
 */

export interface FakeTrack {
  stop: ReturnType<typeof vi.fn>;
}

export class FakeRecorder {
  static instances: FakeRecorder[] = [];
  /** The types `isTypeSupported` accepts: Chrome's, by default. */
  static supported = new Set(["audio/webm;codecs=opus", "audio/ogg;codecs=opus"]);
  /** What a browser records when it is not told a type (Safari). */
  static fallbackType = "audio/mp4";

  static isTypeSupported(type: string) {
    return FakeRecorder.supported.has(type);
  }

  state: "inactive" | "recording" = "inactive";
  mimeType: string;
  ondataavailable: ((event: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  onerror: ((event: Event) => void) | null = null;

  constructor(
    readonly stream: MediaStream,
    readonly options: MediaRecorderOptions = {},
  ) {
    this.mimeType = options.mimeType ?? FakeRecorder.fallbackType;
    FakeRecorder.instances.push(this);
  }

  start() {
    this.state = "recording";
  }

  stop() {
    if (this.state === "inactive") throw new DOMException("not recording", "InvalidStateError");
    this.state = "inactive";
    queueMicrotask(() => {
      this.ondataavailable?.({ data: new Blob(["voice"], { type: this.mimeType }) });
      this.onstop?.();
    });
  }

  /** The recording ends by itself, as when the microphone is unplugged. */
  end() {
    this.stop();
  }
}

export interface FakeMicrophone {
  track: FakeTrack;
  stream: MediaStream;
  getUserMedia: ReturnType<typeof vi.fn>;
}

/** Puts MediaRecorder and `navigator.mediaDevices` in place; `reject` makes the microphone refuse. */
export function stubMicrophone(reject?: { name: string }): FakeMicrophone {
  FakeRecorder.instances = [];
  FakeRecorder.supported = new Set(["audio/webm;codecs=opus", "audio/ogg;codecs=opus"]);
  FakeRecorder.fallbackType = "audio/mp4";
  const track: FakeTrack = { stop: vi.fn() };
  const stream = { getTracks: () => [track] } as unknown as MediaStream;
  const getUserMedia = vi.fn(async () => {
    if (reject) throw Object.assign(new Error(reject.name), { name: reject.name });
    return stream;
  });
  vi.stubGlobal("MediaRecorder", FakeRecorder);
  vi.stubGlobal("navigator", { ...navigator, mediaDevices: { getUserMedia } });
  return { track, stream, getUserMedia };
}
