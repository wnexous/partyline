import { vi } from "vitest";

/*
 * A stand-in for the part of HTMLMediaElement an audio player drives, for tests (VOI-07, VOI-08).
 * happy-dom plays nothing: every element starts paused with no length, and a test says what the
 * browser would (`load` once the length is known, `tick` as the time moves, `end`, `fail`).
 */

interface MediaState {
  paused: boolean;
  duration: number;
  currentTime: number;
  /** Every position the page asked the element to go to, in order. */
  seeks: number[];
}

const states = new WeakMap<HTMLMediaElement, MediaState>();

function stateOf(el: HTMLMediaElement): MediaState {
  let state = states.get(el);
  if (!state) {
    state = { paused: true, duration: Number.NaN, currentTime: 0, seeks: [] };
    states.set(el, state);
  }
  return state;
}

export const media = {
  /** The element learned its length (`Infinity` for a recording that carries none). */
  load(el: HTMLMediaElement, duration: number) {
    stateOf(el).duration = duration;
    el.dispatchEvent(new Event("loadedmetadata"));
    el.dispatchEvent(new Event("durationchange"));
  },
  /** The playback position moved. */
  tick(el: HTMLMediaElement, time: number) {
    stateOf(el).currentTime = time;
    el.dispatchEvent(new Event("timeupdate"));
  },
  end(el: HTMLMediaElement) {
    const state = stateOf(el);
    state.paused = true;
    state.currentTime = state.duration;
    el.dispatchEvent(new Event("ended"));
  },
  fail(el: HTMLMediaElement) {
    el.dispatchEvent(new Event("error"));
  },
  seeks: (el: HTMLMediaElement) => stateOf(el).seeks,
  position: (el: HTMLMediaElement) => stateOf(el).currentTime,
  paused: (el: HTMLMediaElement) => stateOf(el).paused,
};

export const play = vi.fn();
export const pause = vi.fn();

/** Replaces the media element's behaviour; undo it with `vi.unstubAllGlobals()` and `restoreMedia()`. */
export function stubMedia() {
  const proto = HTMLMediaElement.prototype;
  const saved = (["paused", "duration", "currentTime", "play", "pause", "load"] as const).map(
    (key) => [key, Object.getOwnPropertyDescriptor(proto, key)] as const,
  );
  play.mockImplementation(function (this: HTMLMediaElement) {
    stateOf(this).paused = false;
    this.dispatchEvent(new Event("play"));
    return Promise.resolve();
  });
  pause.mockImplementation(function (this: HTMLMediaElement) {
    const state = stateOf(this);
    if (state.paused) return;
    state.paused = true;
    this.dispatchEvent(new Event("pause"));
  });
  Object.defineProperties(proto, {
    paused: {
      configurable: true,
      get: function (this: HTMLMediaElement) {
        return stateOf(this).paused;
      },
    },
    duration: {
      configurable: true,
      get: function (this: HTMLMediaElement) {
        return stateOf(this).duration;
      },
    },
    currentTime: {
      configurable: true,
      get: function (this: HTMLMediaElement) {
        return stateOf(this).currentTime;
      },
      set: function (this: HTMLMediaElement, value: number) {
        const state = stateOf(this);
        state.currentTime = value;
        state.seeks.push(value);
        this.dispatchEvent(new Event("timeupdate"));
      },
    },
    play: { configurable: true, value: play },
    pause: { configurable: true, value: pause },
    load: { configurable: true, value: () => {} },
  });
  return () => {
    for (const [key, descriptor] of saved) {
      if (descriptor) Object.defineProperty(proto, key, descriptor);
      else delete (proto as unknown as Record<string, unknown>)[key];
    }
    play.mockReset();
    pause.mockReset();
  };
}
