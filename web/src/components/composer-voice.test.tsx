import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "@/components/composer";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { stubPointer } from "@/test/media";
import { type FakeMicrophone, FakeRecorder, stubMicrophone } from "@/test/recorder";
import { renderWithProviders } from "@/test/render";

/*
 * Recording a voice message in the composer (VOI-01..VOI-06): a microphone button where the browser
 * can record; a tap records until Send or Cancel, a hold records while held; the recording is sent
 * as an audio attachment through the message upload.
 */

const posts = <T extends { method: string; path: string }>(calls: T[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");

const renderComposer = () =>
  renderWithProviders(<Composer roomId="r1" roomName="deploys" level="post" />);

const record = () => screen.getByRole("button", { name: "Record a voice message" });
const sendVoice = () => screen.getByRole("button", { name: "Send voice message" });
const cancelVoice = () => screen.getByRole("button", { name: "Cancel recording" });
const textbox = () => screen.queryByRole("textbox", { name: "Message deploys" });
const wait = (ms: number) => new Promise((done) => setTimeout(done, ms));

/** What the form posted: its message part and its file part. */
function voicePost(calls: ReturnType<typeof mockApi>["calls"]) {
  const body = posts(calls)[0]?.body;
  expect(body).toBeInstanceOf(FormData);
  const form = body as FormData;
  return { message: JSON.parse(String(form.get("message"))), files: form.getAll("file") as File[] };
}

let mic: FakeMicrophone;
beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  stubPointer("fine");
  mic = stubMicrophone();
});
afterEach(() => vi.unstubAllGlobals());

describe("the microphone button (VOI-01)", () => {
  it("is in the composer where the browser can record", () => {
    mockApi({});
    renderComposer();
    expect(record()).toBeInTheDocument();
  });

  it.each([
    ["MediaRecorder", () => vi.stubGlobal("MediaRecorder", undefined)],
    [
      "a microphone API",
      () => vi.stubGlobal("navigator", { ...navigator, mediaDevices: undefined }),
    ],
  ])("is not shown without %s", (_what, remove) => {
    mockApi({});
    remove();
    renderComposer();
    expect(screen.queryByRole("button", { name: "Record a voice message" })).toBeNull();
    // Everything else is where it was.
    expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument();
    expect(screen.getByLabelText("Attach files")).toBeInTheDocument();
  });

  it("is named in Portuguese when the app is", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({});
    renderComposer();
    expect(screen.getByRole("button", { name: "Gravar mensagem de voz" })).toBeInTheDocument();
  });

  it("asks for nothing until it is used: no microphone prompt on opening a conversation", () => {
    mockApi({});
    renderComposer();
    expect(mic.getUserMedia).not.toHaveBeenCalled();
  });
});

describe("a tap records until Send or Cancel (VOI-02)", () => {
  it("asks for the microphone, replaces the text box with a recording bar and offers Cancel and Send", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());

    expect(mic.getUserMedia).toHaveBeenCalledWith({ audio: true });
    expect(await screen.findByText("Recording")).toBeInTheDocument();
    expect(screen.getByRole("timer")).toHaveTextContent("0:00");
    expect(textbox()).toBeNull();
    expect(cancelVoice()).toBeInTheDocument();
    expect(sendVoice()).toBeInTheDocument();
    // The tap that started it is not a Send: nothing was posted and it is still recording.
    expect(FakeRecorder.instances[0]?.state).toBe("recording");
    // The text Send and the files are out of the way while recording.
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    expect(screen.queryByLabelText("Attach files")).toBeNull();
  });

  it("names Cancel in words beside its icon, and Discard once the recording has stopped", async () => {
    mockApi({ "POST /rooms/r1/messages": apiError(500, "internal") });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    // The visible word is part of the accessible name (label in name).
    expect(cancelVoice()).toHaveTextContent("Cancel");
    await user.click(sendVoice());
    await screen.findByRole("alert");
    const discard = screen.getByRole("button", { name: "Discard voice message" });
    expect(discard).toHaveTextContent("Discard");
  });

  it("counts the time up", async () => {
    mockApi({});
    // Only the clock and the interval are faked: the pointer and the promises stay real.
    vi.useFakeTimers({ toFake: ["Date", "setInterval", "clearInterval"] });
    try {
      const user = userEvent.setup();
      renderComposer();
      await user.click(record());
      await screen.findByText("Recording");
      act(() => vi.advanceTimersByTime(2_300));
      expect(screen.getByRole("timer")).toHaveTextContent("0:02");
      act(() => vi.advanceTimersByTime(63_000));
      expect(screen.getByRole("timer")).toHaveTextContent("1:05");
    } finally {
      vi.useRealTimers();
    }
  });

  it("keeps focus in the composer when the text box gives way to the bar", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(textbox() as HTMLElement);
    await user.click(record());
    await screen.findByText("Recording");
    // Not on the page's body: Escape and a screen reader still find the composer.
    expect(sendVoice()).toHaveFocus();
  });

  it("announces that it is recording, and what to do next, to a screen reader", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    // The announcement is set by an effect after the bar is drawn: wait for it, do not race it.
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Recording a voice message. Send it or cancel it when you are done.",
      ),
    );
  });

  it("starts from the keyboard too: Enter starts it, Enter on the same button sends it", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    record().focus();
    await user.keyboard("{Enter}");
    await screen.findByText("Recording");
    expect(sendVoice()).toHaveFocus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(FakeRecorder.instances[0]?.state).toBe("inactive");
  });

  it("starts from the keyboard with Space as well", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    record().focus();
    await user.keyboard(" ");
    expect(await screen.findByText("Recording")).toBeInTheDocument();
  });
});

describe("sending (VOI-04)", () => {
  it("stops, posts the recording as one audio attachment with no text, and hands the box back", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());

    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const { message, files } = voicePost(calls);
    expect(message).toEqual({ text: "" });
    expect(files).toHaveLength(1);
    expect(files[0]?.name).toMatch(/^voice-message-\d{4}-\d{2}-\d{2}-\d{6}\.webm$/);
    expect(files[0]?.type).toBe("audio/webm;codecs=opus");
    expect(await files[0]?.text()).toBe("voice");

    // The bar is gone and the box is back; the microphone was let go.
    await waitFor(() => expect(textbox()).toBeInTheDocument());
    expect(screen.queryByText("Recording")).toBeNull();
    expect(record()).toBeInTheDocument();
    expect(mic.track.stop).toHaveBeenCalled();
  });

  it("gives the text box focus back when it had it, so the keyboard stays open", async () => {
    mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.click(textbox() as HTMLElement);
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());
    await waitFor(() => expect(textbox()).toHaveFocus());
  });

  it("does not open the keyboard when the text box did not have focus: the microphone keeps it", async () => {
    mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());
    await waitFor(() => expect(screen.queryByText("Recording")).toBeNull());
    await waitFor(() => expect(record()).toHaveFocus());
    expect(textbox()).not.toHaveFocus();
  });

  it("leaves the draft alone: a voice message is a message of its own", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.type(textbox() as HTMLElement, "still writing this");
    await user.upload(
      screen.getByLabelText("Attach files"),
      new File(["x"], "draft.log", { type: "text/plain" }),
    );
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());

    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const { message, files } = voicePost(calls);
    expect(message).toEqual({ text: "" });
    expect(files.map((f) => f.name)).toEqual([expect.stringMatching(/^voice-message-/)]);
    await waitFor(() => expect(textbox()).toHaveValue("still writing this"));
    expect(screen.getByText("draft.log")).toBeInTheDocument();
  });

  it("keeps the recording when the send fails, says why, and sends it again on request", async () => {
    let attempts = 0;
    const { calls } = mockApi({
      "POST /rooms/r1/messages": () =>
        ++attempts === 1 ? apiError(413, "too_large") : json(201, makeMessage({ seq: 9 })),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());

    expect(await screen.findByRole("alert")).toHaveTextContent(/too large/i);
    // Stopped, waiting: the length it has, to send again or to throw away.
    expect(screen.getByText("Voice message")).toBeInTheDocument();
    expect(screen.queryByText("Recording")).toBeNull();
    expect(screen.getByRole("button", { name: "Discard voice message" })).toBeInTheDocument();
    expect(mic.track.stop).toHaveBeenCalled();

    await user.click(sendVoice());
    await waitFor(() => expect(posts(calls)).toHaveLength(2));
    await waitFor(() => expect(textbox()).toBeInTheDocument());
    expect(screen.queryByRole("alert")).toBeNull();
    // The second send carried the same recording, not a new one.
    expect(FakeRecorder.instances).toHaveLength(1);
    expect(voicePost(calls).files).toHaveLength(1);
  });

  it("lets the user discard a recording the server refused", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": apiError(403, "forbidden") });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());
    expect(await screen.findByRole("alert")).toHaveTextContent(/not allowed to post/i);

    await user.click(screen.getByRole("button", { name: "Discard voice message" }));
    expect(textbox()).toBeInTheDocument();
    expect(screen.queryByText("Voice message")).toBeNull();
    expect(posts(calls)).toHaveLength(1);
  });

  it("shows a recording that ended by itself, to be sent or discarded", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    // The microphone is unplugged: the recorder stops on its own.
    act(() => (FakeRecorder.instances[0] as FakeRecorder).end());
    expect(await screen.findByText("Voice message")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("status")).toHaveTextContent(
        "Recording stopped. Send it or discard it.",
      ),
    );
    await user.click(sendVoice());
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
  });
});

describe("cancelling (VOI-05)", () => {
  it("throws the recording away, posts nothing and lets go of the microphone", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(cancelVoice());

    expect(textbox()).toBeInTheDocument();
    expect(screen.queryByText("Recording")).toBeNull();
    expect(posts(calls)).toHaveLength(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("status")).toHaveTextContent("Recording discarded.");
  });

  it("is what Escape does", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.keyboard("{Escape}");
    expect(await screen.findByRole("textbox", { name: "Message deploys" })).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
    // The bar had focus; it is not lost with it.
    await waitFor(() => expect(record()).toHaveFocus());
  });

  it("gives the text box focus back when it was typing", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(textbox() as HTMLElement);
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(cancelVoice());
    await waitFor(() => expect(textbox()).toHaveFocus());
  });

  it("lets go of the microphone when the composer closes in the middle of a recording", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { unmount } = renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    unmount();
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });
});

describe("when the microphone is not available (VOI-06)", () => {
  it.each([
    [
      "NotAllowedError",
      /Microphone access is blocked\. Allow it for this site in your browser settings/,
    ],
    ["NotFoundError", /No microphone was found\./],
    ["NotReadableError", /The microphone is in use by another app\./],
    ["TypeError", /Couldn’t record\. Try again\./],
  ])("%s is said in plain words, with no recording bar left behind", async (name, words) => {
    mic = stubMicrophone({ name });
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());

    expect(await screen.findByRole("alert")).toHaveTextContent(words);
    expect(screen.queryByText("Recording")).toBeNull();
    expect(textbox()).toBeInTheDocument();
    expect(record()).toBeEnabled();
    expect(FakeRecorder.instances).toHaveLength(0);
  });

  it("is said in Portuguese when the app is", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mic = stubMicrophone({ name: "NotAllowedError" });
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(screen.getByRole("button", { name: "Gravar mensagem de voz" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "O acesso ao microfone está bloqueado.",
    );
  });

  it("forgets the message once the user tries again and it works", async () => {
    mic = stubMicrophone({ name: "NotAllowedError" });
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByRole("alert");
    mic.getUserMedia.mockImplementationOnce(async () => mic.stream);
    await user.click(record());
    expect(await screen.findByText("Recording")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("a hold records while held (VOI-03)", () => {
  const press = (over: { x?: number } = {}) =>
    fireEvent.pointerDown(record(), {
      pointerType: "touch",
      pointerId: 7,
      button: 0,
      clientX: over.x ?? 300,
    });
  const release = (x = 300) =>
    fireEvent.pointerUp(screen.getByRole("button", { name: /voice message/i }), {
      pointerType: "touch",
      pointerId: 7,
      clientX: x,
    });
  const move = (x: number) =>
    fireEvent.pointerMove(screen.getByRole("button", { name: /voice message/i }), {
      pointerType: "touch",
      pointerId: 7,
      clientX: x,
    });

  it("sends on release, and says how to cancel while it is held", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    renderComposer();
    press();
    await screen.findByText("Recording");
    expect(screen.queryByText("Slide left to cancel")).toBeNull();
    await act(() => wait(560));
    expect(screen.getByText("Slide left to cancel")).toBeInTheDocument();

    release();
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(voicePost(calls).files).toHaveLength(1);
    await waitFor(() => expect(textbox()).toBeInTheDocument());
  });

  it("cancels when the finger slides far enough to the left, saying so before it lets go", async () => {
    const { calls } = mockApi({});
    renderComposer();
    press({ x: 300 });
    await screen.findByText("Recording");
    await act(() => wait(560));

    move(240);
    expect(screen.getByText("Slide left to cancel")).toBeInTheDocument();
    move(215);
    expect(screen.getByText("Release to cancel")).toBeInTheDocument();
    move(250);
    expect(screen.getByText("Slide left to cancel")).toBeInTheDocument();
    move(200);
    expect(screen.getByText("Release to cancel")).toBeInTheDocument();

    release(200);
    await waitFor(() => expect(textbox()).toBeInTheDocument());
    expect(posts(calls)).toHaveLength(0);
    expect(mic.track.stop).toHaveBeenCalledTimes(1);
  });

  it("sends when the finger came back from the edge before it let go", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    renderComposer();
    press({ x: 300 });
    await screen.findByText("Recording");
    await act(() => wait(560));
    move(180);
    expect(screen.getByText("Release to cancel")).toBeInTheDocument();
    move(290);
    release(290);
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
  });

  it("is a tap when let go sooner: the recording stays on, with its buttons, and the click that follows sends nothing", async () => {
    const { calls } = mockApi({});
    renderComposer();
    press();
    await screen.findByText("Recording");
    release();
    // The click a browser sends after a tap must not be taken for a press on Send.
    fireEvent.click(screen.getByRole("button", { name: /voice message/i }));
    await act(() => wait(50));
    expect(screen.getByText("Recording")).toBeInTheDocument();
    expect(cancelVoice()).toBeInTheDocument();
    expect(sendVoice()).toBeInTheDocument();
    expect(screen.queryByText("Slide left to cancel")).toBeNull();
    expect(posts(calls)).toHaveLength(0);
    expect(FakeRecorder.instances[0]?.state).toBe("recording");
  });

  it("does not take the button for Send until the recording is on: a wobble in a tap does not cancel it", async () => {
    mockApi({});
    renderComposer();
    press({ x: 300 });
    await screen.findByText("Recording");
    // A shaky finger within the first half second is not a slide.
    move(150);
    release(150);
    await act(() => wait(50));
    expect(screen.getByText("Recording")).toBeInTheDocument();
    expect(mic.track.stop).not.toHaveBeenCalled();
  });

  it("keeps the on-screen keyboard where it is: the press on the microphone takes no focus", () => {
    mockApi({});
    renderComposer();
    expect(fireEvent.pointerDown(record(), { pointerType: "touch", pointerId: 7, button: 0 })).toBe(
      false,
    );
  });

  it("does not offer the button's cancel on a recording that was already stopped", async () => {
    mockApi({ "POST /rooms/r1/messages": apiError(500, "internal") });
    const user = userEvent.setup();
    renderComposer();
    await user.click(record());
    await screen.findByText("Recording");
    await user.click(sendVoice());
    await screen.findByRole("alert");
    const bar = screen.getByText("Voice message").closest("div") as HTMLElement;
    expect(within(bar.parentElement as HTMLElement).queryByText("Slide left to cancel")).toBeNull();
  });
});
