import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppProviders } from "@/components/app-providers";
import { Composer } from "@/components/composer";
import type { Message } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { stubPointer } from "@/test/media";
import { stubMicrophone } from "@/test/recorder";
import { makeQueryClient, renderWithProviders } from "@/test/render";

/*
 * Answering a message (ACT-07): the composer says whom it is answering, with a line of what they
 * said and a way to take it back, and the next message sent, text or voice, carries `reply_to`.
 */

const target = (over: Partial<Message> = {}) =>
  makeMessage({ id: "m-9", seq: 9, text: "can you check the deploy?", ...over });

const onClear = vi.fn();

const composer = (replyTo: Message | null) => (
  <Composer roomId="r1" roomName="deploys" level="post" replyTo={replyTo} onClearReply={onClear} />
);

function renderComposer(replyTo: Message | null = target()) {
  const queryClient = makeQueryClient();
  const utils = renderWithProviders(composer(replyTo), queryClient);
  return {
    ...utils,
    /** Shows the same composer again with another message to answer. */
    answer: (next: Message | null) =>
      utils.rerender(<AppProviders queryClient={queryClient}>{composer(next)}</AppProviders>),
  };
}

const textbox = () => screen.getByRole("textbox", { name: "Message deploys" });
const posts = (calls: ReturnType<typeof mockApi>["calls"]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  stubPointer("fine");
  onClear.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the strip above the text box", () => {
  it("says whom the next message answers, with a line of what they said", () => {
    mockApi({});
    renderComposer();
    const strip = screen.getByTestId("reply-strip");
    expect(within(strip).getByText("Replying to Ana")).toBeInTheDocument();
    expect(within(strip).getByText("can you check the deploy?")).toBeInTheDocument();
  });

  it("is not there when nothing is being answered", () => {
    mockApi({});
    renderComposer(null);
    expect(screen.queryByTestId("reply-strip")).toBeNull();
  });

  it("names a recording as one, and a file by its name", () => {
    mockApi({});
    const voice = {
      id: "v1",
      name: "voice-message-2026-10-24-140509.webm",
      mime: "audio/webm",
      size: 5,
      status: "stored" as const,
    };
    const { unmount } = renderComposer(target({ text: "", attachments: [voice] }));
    expect(screen.getByTestId("reply-strip")).toHaveTextContent("Voice message");
    unmount();
    renderComposer(target({ text: "", attachments: [{ ...voice, name: "notes.pdf" }] }));
    expect(screen.getByTestId("reply-strip")).toHaveTextContent("notes.pdf");
  });

  it("keeps one line: a long message is cut", () => {
    mockApi({});
    renderComposer(target({ text: "word ".repeat(100) }));
    const excerpt = within(screen.getByTestId("reply-strip")).getByText(/^word word/);
    expect(excerpt.textContent?.endsWith("…")).toBe(true);
    expect(excerpt).toHaveClass("truncate");
  });

  it("goes with the text box to a screen reader: it describes it", () => {
    mockApi({});
    renderComposer();
    expect(textbox()).toHaveAccessibleDescription(/Replying to Ana/);
  });

  it("is in Portuguese when the app is", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({});
    renderComposer();
    expect(screen.getByText("Respondendo a Ana")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancelar resposta" })).toBeInTheDocument();
  });

  it("puts the cursor in the text box as soon as a message is chosen to answer", async () => {
    mockApi({});
    const { answer } = renderComposer(null);
    expect(textbox()).not.toHaveFocus();
    answer(target());
    await waitFor(() => expect(textbox()).toHaveFocus());
  });

  it("does not take the cursor back when the same message is still being answered", async () => {
    mockApi({});
    const { answer } = renderComposer();
    await waitFor(() => expect(textbox()).toHaveFocus());
    screen.getByRole("button", { name: "Cancel reply" }).focus();
    answer(target({ text: "edited" }));
    expect(textbox()).not.toHaveFocus();
  });
});

describe("taking it back", () => {
  it("goes with the Cancel reply button, which leaves the cursor where it was", async () => {
    mockApi({});
    renderComposer();
    await waitFor(() => expect(textbox()).toHaveFocus());
    const cancel = screen.getByRole("button", { name: "Cancel reply" });
    // A press on it must not close the on-screen keyboard.
    expect(fireEvent.pointerDown(cancel, { pointerType: "touch", button: 0 })).toBe(false);
    expect(fireEvent.mouseDown(cancel)).toBe(false);
    await userEvent.click(cancel);
    expect(onClear).toHaveBeenCalledTimes(1);
    expect(textbox()).toHaveFocus();
  });

  it("goes with Escape in the text box", async () => {
    mockApi({});
    renderComposer();
    await waitFor(() => expect(textbox()).toHaveFocus());
    await userEvent.keyboard("{Escape}");
    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it("is not what Escape does with nothing to answer", async () => {
    mockApi({});
    renderComposer(null);
    await userEvent.type(textbox(), "x{Escape}");
    expect(onClear).not.toHaveBeenCalled();
  });

  it("keeps what was typed", async () => {
    mockApi({});
    renderComposer();
    await userEvent.type(textbox(), "draft{Escape}");
    expect(textbox()).toHaveValue("draft");
  });
});

describe("the message sent answers it (ACT-07)", () => {
  it("carries reply_to, and the strip goes once it is sent", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 10 })) });
    renderComposer();
    await userEvent.type(textbox(), "yes, it is green");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "yes, it is green", reply_to: "m-9" });
    await waitFor(() => expect(onClear).toHaveBeenCalledTimes(1));
  });

  it("carries it in the message part of an upload", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 10 })) });
    renderComposer();
    await userEvent.upload(
      screen.getByLabelText("Attach files"),
      new File(["x"], "log.txt", { type: "text/plain" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const form = posts(calls)[0]?.body as FormData;
    expect(JSON.parse(String(form.get("message")))).toEqual({ text: "", reply_to: "m-9" });
  });

  it("sends a plain message with no reply_to at all when nothing is answered", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 10 })) });
    renderComposer(null);
    await userEvent.type(textbox(), "hello");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "hello" });
    expect(onClear).not.toHaveBeenCalled();
  });

  it("stays on the message when the send fails, to try again", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": apiError(500, "internal") });
    renderComposer();
    await userEvent.type(textbox(), "will fail");
    await userEvent.click(screen.getByRole("button", { name: "Send" }));
    await screen.findByRole("alert");
    expect(onClear).not.toHaveBeenCalled();
    expect(screen.getByTestId("reply-strip")).toBeInTheDocument();
    expect(posts(calls)).toHaveLength(1);
  });

  it("carries it for a voice message too", async () => {
    stubMicrophone();
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 10 })) });
    renderComposer();
    await userEvent.click(screen.getByRole("button", { name: "Record a voice message" }));
    await screen.findByText("Recording");
    await userEvent.click(screen.getByRole("button", { name: "Send voice message" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const form = posts(calls)[0]?.body as FormData;
    expect(JSON.parse(String(form.get("message")))).toEqual({ text: "", reply_to: "m-9" });
    await waitFor(() => expect(onClear).toHaveBeenCalledTimes(1));
  });

  it("keeps the reply when a voice message fails to send", async () => {
    stubMicrophone();
    mockApi({ "POST /rooms/r1/messages": apiError(413, "too_large") });
    renderComposer();
    await userEvent.click(screen.getByRole("button", { name: "Record a voice message" }));
    await screen.findByText("Recording");
    await userEvent.click(screen.getByRole("button", { name: "Send voice message" }));
    await screen.findByRole("alert");
    expect(onClear).not.toHaveBeenCalled();
  });
});
