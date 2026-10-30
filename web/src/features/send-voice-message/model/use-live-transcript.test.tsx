import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/shared/i18n";
import { FakeRecognition, removeSpeech, stubSpeech } from "@/test/speech";
import { useLiveTranscript } from "./use-live-transcript";

/*
 * The live transcript of a recording (VOI-09): the Web Speech API listens while the microphone
 * records, the interim text is shown as it arrives, the final text is what the message carries,
 * and a browser without the API (or one that refuses it) sends the audio alone.
 */

beforeEach(stubSpeech);
afterEach(() => vi.unstubAllGlobals());

const render = (active = true) =>
  renderHook(({ on }) => useLiveTranscript(on), {
    initialProps: { on: active },
  });
const last = () =>
  FakeRecognition.instances[FakeRecognition.instances.length - 1] as FakeRecognition;

describe("listening while recording (VOI-09)", () => {
  it("asks for the app's language, continuous speech and the words still changing", () => {
    render();
    expect(FakeRecognition.instances).toHaveLength(1);
    const rec = last();
    expect(rec.started).toBe(true);
    expect(rec.lang).toBe("en");
    expect(rec.continuous).toBe(true);
    expect(rec.interimResults).toBe(true);
  });

  it("shows the interim text and keeps the final one for the message", () => {
    const hook = render();
    act(() => last().say([], "deploy is"));
    expect(hook.result.current.text).toBe("deploy is");
    expect(hook.result.current.final).toBe("");

    act(() => last().say(["deploy is green"], ""));
    expect(hook.result.current.text).toBe("deploy is green");
    expect(hook.result.current.final).toBe("deploy is green");
  });

  it("adds the finals that arrive in pieces without repeating them", () => {
    const hook = render();
    act(() => last().say(["deploy is"]));
    act(() => last().say(["green"], "and the tests"));
    expect(hook.result.current.final).toBe("deploy is green");
    expect(hook.result.current.text).toBe("deploy is green and the tests");
  });

  it("stops when the recording stops, keeping the last words", async () => {
    const hook = render();
    act(() => last().say(["the release is green"]));
    const rec = last();
    act(() => hook.rerender({ on: false }));
    expect(rec.started).toBe(false);
    // An `end` after the recording stopped starts nothing again.
    act(() => rec.onend?.());
    expect(FakeRecognition.instances).toHaveLength(1);
    await expect(hook.result.current.finish()).resolves.toBe("the release is green");
  });

  it("starts itself again while recording when the browser ends a session on a silence", () => {
    render();
    act(() => last().onend?.());
    expect(FakeRecognition.instances).toHaveLength(2);
    expect(last().started).toBe(true);
  });

  it("waits for the last final result when it is asked to finish, then hands over the text", async () => {
    const hook = render();
    act(() => last().say([], "almost"));
    const rec = last();
    const finishing = hook.result.current.finish();
    // The browser's stop hands over the final word and then ends.
    act(() => rec.say(["almost done"]));
    act(() => rec.onend?.());
    await expect(finishing).resolves.toBe("almost done");
  });

  it("is in Portuguese when the app is", () => {
    renderHook(() => useLiveTranscript(true), {
      wrapper: ({ children }) => <I18nProvider locale="pt-BR">{children}</I18nProvider>,
    });
    expect(last().lang).toBe("pt-BR");
  });
});

describe("a browser without the API (VOI-09)", () => {
  beforeEach(removeSpeech);

  it("listens to nothing, says nothing scary, and sends the audio alone", async () => {
    const hook = render();
    expect(hook.result.current.supported).toBe(false);
    expect(hook.result.current.text).toBe("");
    expect(hook.result.current.unavailable).toBe(false);
    await expect(hook.result.current.finish()).resolves.toBe("");
    expect(FakeRecognition.instances).toHaveLength(0);
  });
});

describe("when recognition fails (VOI-09)", () => {
  it("gives up for this recording, says it quietly and keeps no text", async () => {
    const hook = render();
    act(() => last().say([], "half a sen"));
    act(() => last().fail("not-allowed"));
    expect(hook.result.current.unavailable).toBe(true);
    expect(hook.result.current.final).toBe("");
    // It does not start again for this recording.
    act(() => last().onend?.());
    expect(FakeRecognition.instances).toHaveLength(1);
    await expect(hook.result.current.finish()).resolves.toBe("");
  });

  it("treats a silence as an ordinary end, not a failure", async () => {
    const hook = render();
    await act(async () => {
      last().fail("no-speech");
      await Promise.resolve();
    });
    expect(hook.result.current.unavailable).toBe(false);
    expect(FakeRecognition.instances).toHaveLength(2);
  });

  it("forgets the failure and the words when a new recording starts", () => {
    const hook = render();
    act(() => last().say(["old words"]));
    act(() => last().fail("network"));
    expect(hook.result.current.unavailable).toBe(true);

    act(() => hook.result.current.reset());
    expect(hook.result.current.text).toBe("");
    expect(hook.result.current.final).toBe("");
    expect(hook.result.current.unavailable).toBe(false);
  });
});
