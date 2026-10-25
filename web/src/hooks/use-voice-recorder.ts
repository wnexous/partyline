import { useCallback, useEffect, useRef, useState } from "react";
import {
  AUDIO_BITS_PER_SECOND,
  canRecord,
  MAX_RECORDING_MS,
  micFailure,
  pickRecorderMime,
  type VoiceFailure,
  voiceFileName,
} from "@/lib/voice";

/*
 * The recorder behind the microphone button (VOI-01..VOI-06). It asks for the microphone, records
 * with MediaRecorder in the format the browser can (WebM/Opus, Ogg/Opus, else whatever it records),
 * counts the time, and lets go of the microphone the moment the recording ends, whichever way:
 * sent, cancelled, at the time limit, a failure, or the composer closing.
 */

/** A recording that waits to be sent. */
export interface VoiceRecording {
  file: File;
  durationMs: number;
}

export type VoicePhase =
  /** Nothing is recording. */
  | "idle"
  /** The microphone was asked for and the answer is not in yet (the browser's prompt). */
  | "requesting"
  | "recording"
  /** Stopped, and waiting to be sent or discarded. */
  | "recorded";

export interface VoiceRecorder {
  /** False where there is no MediaRecorder or no microphone API: the button is not shown (VOI-01). */
  supported: boolean;
  phase: VoicePhase;
  /** How long the recording has run; for a finished one, how long it is. */
  elapsedMs: number;
  /** The recording that waits to be sent (phase "recorded"). */
  recording: VoiceRecording | null;
  /** Why the last try did not record anything; cleared by the next. */
  failure: VoiceFailure | null;
  /** Asks for the microphone and starts recording. Does nothing while a recording is on or waits. */
  start: () => Promise<void>;
  /** Stops and resolves with the recording; with nothing when the recording was cancelled. */
  stop: () => Promise<VoiceRecording | null>;
  /** Throws the recording away, a waiting one too, and lets go of the microphone. */
  cancel: () => void;
  /** Forgets the failure that was being shown (the user moved on to something else). */
  clearFailure: () => void;
}

/** One recording in progress; kept out of React state, which only shows it. */
interface Session {
  stream: MediaStream;
  recorder: MediaRecorder;
  chunks: Blob[];
  startedAt: number;
  cancelled: boolean;
  released: boolean;
  tick: number;
  limit: number;
  /** The `stop()` calls waiting for the recording. */
  waiting: ((recording: VoiceRecording | null) => void)[];
}

const TICK_MS = 250;

/** Lets go of the microphone (once): its indicator goes out and other apps can have it. */
function teardown(session: Session) {
  window.clearInterval(session.tick);
  window.clearTimeout(session.limit);
  if (session.released) return;
  session.released = true;
  for (const track of session.stream.getTracks()) track.stop();
}

export function useVoiceRecorder(): VoiceRecorder {
  const [supported] = useState(canRecord);
  const [phase, setPhaseState] = useState<VoicePhase>("idle");
  const [elapsedMs, setElapsedMs] = useState(0);
  const [recording, setRecordingState] = useState<VoiceRecording | null>(null);
  const [failure, setFailure] = useState<VoiceFailure | null>(null);

  // What the callbacks read: the state above is for drawing, these are for deciding.
  const phaseRef = useRef<VoicePhase>("idle");
  const recordingRef = useRef<VoiceRecording | null>(null);
  const session = useRef<Session | null>(null);
  /** Each start and each cancel moves it on; an answer from an older start is let go of. */
  const attempt = useRef(0);
  const mounted = useRef(true);

  const setPhase = useCallback((next: VoicePhase) => {
    phaseRef.current = next;
    if (mounted.current) setPhaseState(next);
  }, []);
  const setRecording = useCallback((next: VoiceRecording | null) => {
    recordingRef.current = next;
    if (mounted.current) setRecordingState(next);
  }, []);

  const fail = useCallback(
    (why: VoiceFailure) => {
      if (mounted.current) setFailure(why);
      setElapsedMs(0);
      setPhase("idle");
    },
    [setPhase],
  );

  /** The recorder stopped: the recording is whole, or it was cancelled, or it came to nothing. */
  const finish = useCallback(
    (s: Session) => {
      teardown(s);
      if (session.current === s) session.current = null;
      const waiting = s.waiting.splice(0);
      if (s.cancelled || !mounted.current) {
        for (const resolve of waiting) resolve(null);
        return;
      }
      const type = s.recorder.mimeType || "audio/webm";
      const file = new File(s.chunks, voiceFileName(type), { type });
      if (file.size === 0) {
        for (const resolve of waiting) resolve(null);
        fail("failed");
        return;
      }
      const done = { file, durationMs: Math.min(Date.now() - s.startedAt, MAX_RECORDING_MS) };
      setRecording(done);
      setElapsedMs(done.durationMs);
      setPhase("recorded");
      for (const resolve of waiting) resolve(done);
    },
    [fail, setPhase, setRecording],
  );

  const stop = useCallback((): Promise<VoiceRecording | null> => {
    const s = session.current;
    if (!s) return Promise.resolve(recordingRef.current);
    return new Promise((resolve) => {
      s.waiting.push(resolve);
      if (s.recorder.state !== "inactive") s.recorder.stop();
    });
  }, []);

  const cancel = useCallback(() => {
    attempt.current += 1;
    const s = session.current;
    if (s) {
      s.cancelled = true;
      session.current = null;
      teardown(s);
      if (s.recorder.state !== "inactive") s.recorder.stop();
      for (const resolve of s.waiting.splice(0)) resolve(null);
    }
    setRecording(null);
    setElapsedMs(0);
    setPhase("idle");
  }, [setPhase, setRecording]);

  const start = useCallback(async () => {
    if (!canRecord() || phaseRef.current !== "idle") return;
    attempt.current += 1;
    const mine = attempt.current;
    setFailure(null);
    setElapsedMs(0);
    setPhase("requesting");

    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch (error) {
      if (attempt.current === mine) fail(micFailure(error));
      return;
    }
    // The user gave up (cancelled, or left) while the browser was asking: the microphone is let go.
    if (attempt.current !== mine || !mounted.current) {
      for (const track of stream.getTracks()) track.stop();
      return;
    }

    let recorder: MediaRecorder;
    try {
      const mimeType = pickRecorderMime((type) => MediaRecorder.isTypeSupported?.(type) ?? false);
      recorder = new MediaRecorder(stream, {
        ...(mimeType ? { mimeType } : {}),
        audioBitsPerSecond: AUDIO_BITS_PER_SECOND,
      });
    } catch {
      for (const track of stream.getTracks()) track.stop();
      fail("failed");
      return;
    }

    const s: Session = {
      stream,
      recorder,
      chunks: [],
      startedAt: Date.now(),
      cancelled: false,
      released: false,
      tick: window.setInterval(() => setElapsedMs(Date.now() - s.startedAt), TICK_MS),
      limit: window.setTimeout(() => void stop(), MAX_RECORDING_MS),
      waiting: [],
    };
    recorder.ondataavailable = (event) => {
      if (event.data.size > 0) s.chunks.push(event.data);
    };
    recorder.onstop = () => finish(s);
    recorder.onerror = () => {
      s.cancelled = true;
      if (session.current === s) session.current = null;
      teardown(s);
      for (const resolve of s.waiting.splice(0)) resolve(null);
      fail("failed");
    };
    session.current = s;
    recorder.start();
    setPhase("recording");
  }, [fail, finish, setPhase, stop]);

  const clearFailure = useCallback(() => setFailure(null), []);

  // The composer closing in the middle of a recording lets go of the microphone.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      attempt.current += 1;
      const s = session.current;
      if (!s) return;
      s.cancelled = true;
      session.current = null;
      teardown(s);
      if (s.recorder.state !== "inactive") s.recorder.stop();
    };
  }, []);

  return { supported, phase, elapsedMs, recording, failure, start, stop, cancel, clearFailure };
}
