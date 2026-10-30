import { useCallback, useEffect, useRef, useState } from "react";
import { useI18n } from "@/shared/i18n";

/*
 * The live transcript of a recording (VOI-09): while the microphone records, the Web Speech API
 * listens to the same voice and the composer shows what it hears, so that the person can check it
 * before sending. The final text goes with the recording as the message text, which spares every
 * AI in the conversation from transcribing the same audio. A browser without the API, or one whose
 * recognition fails (no permission, no network), sends the audio alone, as it did before.
 */

/** The part of the Web Speech API this hook uses; the API is not in the DOM types. */
interface SpeechResult {
  readonly isFinal: boolean;
  readonly 0?: { readonly transcript?: string };
}

interface SpeechResultList {
  readonly length: number;
  readonly [index: number]: SpeechResult | undefined;
}

interface SpeechResultEvent {
  readonly resultIndex: number;
  readonly results: SpeechResultList;
}

interface SpeechErrorEvent {
  readonly error?: string;
}

interface SpeechRecognitionLike {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: SpeechResultEvent) => void) | null;
  onerror: ((event: SpeechErrorEvent) => void) | null;
  onend: (() => void) | null;
  start: () => void;
  stop: () => void;
  abort: () => void;
}

type SpeechRecognitionConstructor = new () => SpeechRecognitionLike;

/** How long a stop waits for the browser's last final result before the recording is sent anyway. */
const FINAL_WAIT_MS = 700;
/** A recognition that ignores `stop()` is let go of after this long, so it never keeps listening. */
const STOP_GRACE_MS = 1_500;

/** The errors that are an ordinary end of a listening session, not a reason to give up. */
const BENIGN_ERRORS = new Set(["no-speech", "aborted"]);

/** The constructor this browser has, or null where it has none (Firefox, and some others). */
function speechRecognition(): SpeechRecognitionConstructor | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as {
    SpeechRecognition?: SpeechRecognitionConstructor;
    webkitSpeechRecognition?: SpeechRecognitionConstructor;
  };
  return w.SpeechRecognition ?? w.webkitSpeechRecognition ?? null;
}

/** Joins two pieces of heard text with one space, without doubling the spaces the API sends. */
function join(current: string, next: string): string {
  const a = current.trim();
  const b = next.trim();
  if (!a) return b;
  if (!b) return a;
  return `${a} ${b}`;
}

export interface LiveTranscript {
  /** False where the browser has no Web Speech API: nothing is shown and the audio goes alone. */
  supported: boolean;
  /** What the browser heard so far, final and interim together: what the recording bar shows. */
  text: string;
  /** Only the final text: what the message carries once the recording is sent. */
  final: string;
  /** The API exists but refused or failed (permission, network): one muted line, never an alert. */
  unavailable: boolean;
  /** Stops listening and resolves with the final text, waiting briefly for the last result. */
  finish: () => Promise<string>;
  /** Stops listening and forgets everything heard: a new recording, or a cancelled one. */
  reset: () => void;
}

export function useLiveTranscript(active: boolean): LiveTranscript {
  const { locale } = useI18n();
  const [Ctor] = useState(speechRecognition);
  const [text, setText] = useState("");
  const [final, setFinal] = useState("");
  const [unavailable, setUnavailable] = useState(false);
  const finalRef = useRef("");
  const interimRef = useRef("");
  const recognition = useRef<SpeechRecognitionLike | null>(null);
  const activeRef = useRef(active);
  const mounted = useRef(true);
  /** The recording is ending: a recognition that ends now must not start itself again. */
  const stopping = useRef(false);
  /** The API failed: it must not be started again for this recording. */
  const failed = useRef(false);
  /** The `finish()` waiting for the last result, if any. */
  const waiting = useRef<(() => void) | null>(null);

  const publish = useCallback(() => {
    if (!mounted.current) return;
    setText(join(finalRef.current, interimRef.current));
    setFinal(finalRef.current);
  }, []);

  /** What one event of the API adds: finals accumulate, the interim is the whole current one. */
  const receive = useCallback(
    (event: SpeechResultEvent) => {
      let interim = "";
      for (let i = 0; i < event.results.length; i += 1) {
        const result = event.results[i];
        const heard = result?.[0]?.transcript ?? "";
        if (result?.isFinal) {
          if (i >= event.resultIndex) finalRef.current = join(finalRef.current, heard);
        } else {
          interim = join(interim, heard);
        }
      }
      interimRef.current = interim;
      publish();
    },
    [publish],
  );

  const startRef = useRef<() => void>(() => {});
  const start = useCallback(() => {
    if (!Ctor) return;
    const previous = recognition.current;
    if (previous) {
      recognition.current = null;
      try {
        previous.abort();
      } catch {
        // A recognition that is already gone: nothing to abort.
      }
    }
    stopping.current = false;
    failed.current = false;
    const rec = new Ctor();
    rec.lang = locale;
    rec.continuous = true;
    rec.interimResults = true;
    rec.onresult = receive;
    rec.onerror = (event) => {
      if (BENIGN_ERRORS.has(event.error ?? "")) return;
      failed.current = true;
      if (mounted.current) setUnavailable(true);
    };
    rec.onend = () => {
      if (recognition.current !== rec) return;
      recognition.current = null;
      const done = waiting.current;
      if (done) {
        waiting.current = null;
        done();
      }
      // Continuous listening still ends after a silence in some browsers: it starts again while
      // the recording is on, and never after it stopped, failed, or asked for its last result.
      if (activeRef.current && !stopping.current && !failed.current) startRef.current();
    };
    recognition.current = rec;
    try {
      rec.start();
    } catch {
      recognition.current = null;
      failed.current = true;
      if (mounted.current) setUnavailable(true);
    }
  }, [Ctor, locale, receive]);
  startRef.current = start;

  /** Stops gracefully, so that the last words the browser heard still arrive. */
  const stop = useCallback(() => {
    stopping.current = true;
    const rec = recognition.current;
    if (!rec) return;
    try {
      rec.stop();
    } catch {
      recognition.current = null;
      try {
        rec.abort();
      } catch {
        // A recognition that is already gone: nothing to abort.
      }
      return;
    }
    window.setTimeout(() => {
      if (recognition.current !== rec) return;
      recognition.current = null;
      try {
        rec.abort();
      } catch {
        // A recognition that is already gone: nothing to abort.
      }
    }, STOP_GRACE_MS);
  }, []);

  const finish = useCallback((): Promise<string> => {
    const rec = recognition.current;
    if (!rec) return Promise.resolve(finalRef.current);
    stopping.current = true;
    return new Promise((resolve) => {
      let settled = false;
      const complete = () => {
        if (settled) return;
        settled = true;
        waiting.current = null;
        window.clearTimeout(timer);
        // A recognition that never ended must not keep the microphone's words coming.
        if (recognition.current === rec) {
          recognition.current = null;
          try {
            rec.abort();
          } catch {
            // A recognition that is already gone: nothing to abort.
          }
        }
        resolve(finalRef.current);
      };
      waiting.current = complete;
      const timer = window.setTimeout(complete, FINAL_WAIT_MS);
      try {
        rec.stop();
      } catch {
        complete();
      }
    });
  }, []);

  const reset = useCallback(() => {
    stopping.current = true;
    failed.current = false;
    const rec = recognition.current;
    recognition.current = null;
    if (rec) {
      try {
        rec.abort();
      } catch {
        // A recognition that is already gone: nothing to abort.
      }
    }
    finalRef.current = "";
    interimRef.current = "";
    if (mounted.current) {
      setText("");
      setFinal("");
      setUnavailable(false);
    }
    waiting.current?.();
  }, []);

  // Listening follows the recording: it starts when the microphone does, and stops (keeping the
  // last words) the moment the recording stops. It never runs while nothing is being recorded.
  useEffect(() => {
    activeRef.current = active;
    if (active) startRef.current();
    else stop();
  }, [active, stop]);

  // Leaving the composer ends a recognition too, and lets go of a `finish()` that was waiting.
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      const rec = recognition.current;
      recognition.current = null;
      if (rec) {
        try {
          rec.abort();
        } catch {
          // A recognition that is already gone: nothing to abort.
        }
      }
      waiting.current?.();
    };
  }, []);

  return { supported: Ctor !== null, text, final, unavailable, finish, reset };
}
