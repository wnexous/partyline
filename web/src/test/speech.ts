import { vi } from "vitest";

/*
 * A stand-in for the Web Speech API, for tests (VOI-09). It records what it was asked for, and
 * `say()` hands over results as the browser does: a list of final results and one interim one.
 * Nothing listens: `stop()` fires `end` a microtask later, after the last final result.
 */

interface FakeResult {
  readonly isFinal: boolean;
  readonly 0: { readonly transcript: string };
}

export class FakeRecognition {
  static instances: FakeRecognition[] = [];

  lang = "";
  continuous = false;
  interimResults = false;
  started = false;
  onresult: ((event: { resultIndex: number; results: FakeResult[] }) => void) | null = null;
  onerror: ((event: { error: string }) => void) | null = null;
  onend: (() => void) | null = null;

  constructor() {
    FakeRecognition.instances.push(this);
  }

  start() {
    this.started = true;
  }

  stop() {
    this.started = false;
    queueMicrotask(() => this.onend?.());
  }

  abort() {
    this.started = false;
  }

  /** Hands over the final pieces heard so far, and the interim one that is still changing. */
  say(final: string[], interim = "") {
    const results: FakeResult[] = final.map((transcript) => ({
      isFinal: true,
      0: { transcript },
    }));
    if (interim) results.push({ isFinal: false, 0: { transcript: interim } });
    this.onresult?.({ resultIndex: 0, results });
  }

  /** The API refused or failed (no permission, no network). */
  fail(error = "not-allowed") {
    this.onerror?.({ error });
    this.started = false;
    queueMicrotask(() => this.onend?.());
  }
}

/** Puts the Web Speech API in place and forgets the recognitions of an earlier test. */
export function stubSpeech(): void {
  FakeRecognition.instances = [];
  vi.stubGlobal("SpeechRecognition", FakeRecognition);
  vi.stubGlobal("webkitSpeechRecognition", undefined);
}

/** A browser without the API (Firefox, and some others). */
export function removeSpeech(): void {
  FakeRecognition.instances = [];
  vi.stubGlobal("SpeechRecognition", undefined);
  vi.stubGlobal("webkitSpeechRecognition", undefined);
}
