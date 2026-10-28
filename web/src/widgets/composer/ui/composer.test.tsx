import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppProviders } from "@/components/app-providers";
import type { MessagePages } from "@/shared/api/cache-types";
import { queryKeys } from "@/shared/api/query-keys";
import { type Level, MaxTextLength, type Message, type RoomMember } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeMember, makeMessage } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { stubMatchMedia, stubPointer } from "@/test/media";
import { makeQueryClient, renderWithProviders } from "@/test/render";
import { Composer } from "@/widgets/composer";

// A room may still carry chat members from before the upgrade; the composer ignores them.
const chatMembers = [
  makeMember({ id: "mb-ops", kind: "chat", member_id: "c1", name: "Ops on call" }),
];

function renderComposer(level: Level = "post", members?: RoomMember[]) {
  return renderWithProviders(
    <Composer roomId="r1" roomName="deploys" level={level} members={members} />,
  );
}

const posts = <T extends { method: string; path: string }>(calls: T[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/rooms/r1/messages");

/** A clipboard or a drag carrying files, the way a browser hands them to the composer. */
const withFiles = (...files: File[]) => {
  const data = new DataTransfer();
  for (const file of files) data.items.add(file);
  return data;
};

const fileOf = (name: string, type: string, size = 120 * 1024) =>
  new File([new Uint8Array(size)], name, { type });

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("composer (UI-07)", () => {
  it("sends the trimmed text on Enter and clears the box", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "  ship it  {Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "ship it" });
    await waitFor(() => expect(box).toHaveValue(""));
  });

  it("adds a newline on Shift+Enter without sending", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "line one{Shift>}{Enter}{/Shift}line two");
    expect(box).toHaveValue("line one\nline two");
    expect(posts(calls)).toHaveLength(0);
  });

  it("does not send blank text", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.type(screen.getByRole("textbox", { name: "Message deploys" }), "   {Enter}");
    expect(posts(calls)).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });

  it("disables sending over MaxTextLength and shows the counter", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.click(box);
    await user.paste("x".repeat(MaxTextLength + 1));
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    expect(screen.getByTestId("composer-counter")).toHaveTextContent("16,001 / 16,000");
    await user.keyboard("{Enter}");
    expect(posts(calls)).toHaveLength(0);
    expect(box).toHaveAttribute("aria-invalid", "true");
  });

  it("allows exactly MaxTextLength characters", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    await user.click(screen.getByRole("textbox", { name: "Message deploys" }));
    await user.paste("y".repeat(MaxTextLength));
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
  });

  it("counts Unicode characters like the server, not UTF-16 units", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.click(box);
    // Each emoji is one character for the server (rune count) but two UTF-16 units.
    await user.paste("😀".repeat(MaxTextLength));
    expect(screen.getByTestId("composer-counter")).toHaveTextContent("16,000 / 16,000");
    expect(box).not.toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const sent = posts(calls)[0]?.body as { text: string } | undefined;
    expect([...(sent?.text ?? "")]).toHaveLength(MaxTextLength);
  });

  it("rejects one Unicode character over MaxTextLength", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.click(box);
    await user.paste("😀".repeat(MaxTextLength + 1));
    expect(screen.getByTestId("composer-counter")).toHaveTextContent("16,001 / 16,000");
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
    await user.keyboard("{Enter}");
    expect(posts(calls)).toHaveLength(0);
  });

  it("shows a read-only notice instead of the composer for view-only users", () => {
    mockApi({});
    renderComposer("view");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.getByText(/You can read this room but not post/)).toBeInTheDocument();
  });

  it("shows the composer to managers", () => {
    mockApi({});
    renderComposer("manage");
    expect(screen.getByRole("textbox", { name: "Message deploys" })).toBeInTheDocument();
  });

  it("offers no chat recipients and posts without recipients (CID-22)", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer("post", chatMembers);
    expect(screen.queryByRole("button", { name: /^To:/ })).toBeNull();
    expect(screen.queryByText(/chats with outbound on/)).toBeNull();
    await user.type(screen.getByRole("textbox", { name: "Message deploys" }), "hello{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "hello" });
  });

  it("uploads attached files as multipart with the message part", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    const file = new File(["log line"], "build.log", { type: "text/plain" });
    await user.upload(screen.getByLabelText("Attach files"), file);
    expect(screen.getByText("build.log")).toBeInTheDocument();
    // Files alone are a valid message.
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const body = posts(calls)[0]?.body;
    expect(body).toBeInstanceOf(FormData);
    const form = body as FormData;
    expect(JSON.parse(String(form.get("message")))).toEqual({ text: "" });
    expect((form.get("file") as File).name).toBe("build.log");
    await waitFor(() => expect(screen.queryByText("build.log")).toBeNull());
  });

  it("keeps the text and explains when sending fails", async () => {
    mockApi({ "POST /rooms/r1/messages": apiError(403, "forbidden") });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "hello{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent(/not allowed to post/i);
    expect(box).toHaveValue("hello");
  });
});

describe("the draft is kept per conversation (WA-07)", () => {
  const box = (room = "deploys") => screen.getByRole("textbox", { name: `Message ${room}` });
  const composerFor = (roomId: string, roomName: string) => (
    <Composer key={roomId} roomId={roomId} roomName={roomName} level="post" />
  );

  it("shows the text and the attachment again after another conversation and back", async () => {
    mockApi({});
    const user = userEvent.setup();
    const queryClient = makeQueryClient();
    const at = (roomId: string, roomName: string) => (
      <AppProviders queryClient={queryClient}>{composerFor(roomId, roomName)}</AppProviders>
    );
    const { rerender } = renderWithProviders(composerFor("r1", "deploys"), queryClient);
    await user.type(box(), "ship it");
    await user.upload(
      screen.getByLabelText("Attach files"),
      new File(["log line"], "build.log", { type: "text/plain" }),
    );

    rerender(at("r2", "alerts"));
    expect(box("alerts")).toHaveValue("");
    expect(screen.queryByText("build.log")).toBeNull();
    await user.type(box("alerts"), "other thing");

    rerender(at("r1", "deploys"));
    expect(box()).toHaveValue("ship it");
    expect(screen.getByText("build.log")).toBeInTheDocument();
    rerender(at("r2", "alerts"));
    expect(box("alerts")).toHaveValue("other thing");
  });

  it("a successful send clears that conversation's draft and leaves the others", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    const queryClient = makeQueryClient();
    const { rerender } = renderWithProviders(composerFor("r2", "alerts"), queryClient);
    await user.type(box("alerts"), "keep me");
    rerender(<AppProviders queryClient={queryClient}>{composerFor("r1", "deploys")}</AppProviders>);
    await user.type(box(), "send me{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    await waitFor(() => expect(box()).toHaveValue(""));
    rerender(<AppProviders queryClient={queryClient}>{composerFor("r2", "alerts")}</AppProviders>);
    expect(box("alerts")).toHaveValue("keep me");
  });

  it("clears the draft of a post that went through after the composer was gone", async () => {
    let finish = () => {};
    const { calls } = mockApi({
      "POST /rooms/r1/messages": () =>
        new Promise<Response>((resolve) => {
          finish = () => resolve(json(201, makeMessage({ seq: 9 })));
        }),
    });
    const user = userEvent.setup();
    const queryClient = makeQueryClient();
    const { rerender } = renderWithProviders(composerFor("r1", "deploys"), queryClient);
    await user.type(box(), "on its way");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    rerender(<AppProviders queryClient={queryClient}>{composerFor("r2", "alerts")}</AppProviders>);
    finish();
    await act(async () => {});
    rerender(<AppProviders queryClient={queryClient}>{composerFor("r1", "deploys")}</AppProviders>);
    await waitFor(() => expect(box()).toHaveValue(""));
  });
});

describe("mentioning a session (SESS-33)", () => {
  const sessions = [
    { label: "", harness: "claude", connected_at: "2026-10-19T11:00:00Z" },
    { label: "claude-2", harness: "pi", connected_at: "2026-10-19T12:00:00Z" },
  ];
  const atlas = makeMember({
    kind: "agent",
    member_id: "a1",
    name: "Atlas",
    intelligence: makeAI(),
    sessions,
  });

  it("inserts @handle:label for a labelled session, with the caret after the space", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer("post", [atlas]);
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "@at");
    expect(screen.getAllByRole("option")).toHaveLength(2);
    // The principal is the first candidate; the labelled session is the second.
    await user.keyboard("{ArrowDown}{Enter}");
    expect(box).toHaveValue("@atlas:claude-2 ");
    expect((box as HTMLTextAreaElement).selectionStart).toBe("@atlas:claude-2 ".length);
    expect(posts(calls)).toHaveLength(0);

    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "@atlas:claude-2" });
  });

  it("inserts @handle for the AI itself", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer("post", [atlas]);
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "@at");
    await user.keyboard("{Enter}");
    expect(box).toHaveValue("@atlas ");
    expect((box as HTMLTextAreaElement).selectionStart).toBe("@atlas ".length);

    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "@atlas" });
  });
});

describe("Enter on touch devices (ENT-01..ENT-05)", () => {
  const sendsOnEnter = "Enter to send · Shift+Enter for a new line";
  const newLineOnEnter = "Enter for a new line · tap Send to send";

  const touchDevices: [string, () => void][] = [
    ["a coarse pointer", () => stubPointer("coarse")],
    ["a device that cannot hover", () => stubMatchMedia((query) => query === "(hover: none)")],
  ];

  describe.each(touchDevices)("on %s", (_name, touch) => {
    it("ENT-01: Enter adds a new line and does not send; the Send button sends", async () => {
      touch();
      const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
      const user = userEvent.setup();
      renderComposer();
      const box = screen.getByRole("textbox", { name: "Message deploys" });

      await user.type(box, "line one{Enter}line two{Enter}");
      expect(box).toHaveValue("line one\nline two\n");
      expect(posts(calls)).toHaveLength(0);

      await user.click(screen.getByRole("button", { name: "Send" }));
      await waitFor(() => expect(posts(calls)).toHaveLength(1));
      // The lines are part of the text; the trailing one is trimmed like any trailing space.
      expect(posts(calls)[0]?.body).toEqual({ text: "line one\nline two" });
      await waitFor(() => expect(box).toHaveValue(""));
    });

    it("ENT-01: Shift+Enter is a new line too", async () => {
      touch();
      const { calls } = mockApi({});
      const user = userEvent.setup();
      renderComposer();
      const box = screen.getByRole("textbox", { name: "Message deploys" });
      await user.type(box, "a{Shift>}{Enter}{/Shift}b");
      expect(box).toHaveValue("a\nb");
      expect(posts(calls)).toHaveLength(0);
    });

    it("ENT-03: the hint says Enter is a new line and the Send button sends", () => {
      touch();
      mockApi({});
      renderComposer();
      expect(screen.getByText(newLineOnEnter)).toBeInTheDocument();
      expect(screen.queryByText(sendsOnEnter)).toBeNull();
    });

    it("ENT-03: the hint wraps to a second line beside the three buttons, never cut with an ellipsis", () => {
      touch();
      mockApi({});
      renderComposer();
      const hint = screen.getByText(newLineOnEnter);
      expect(hint).toHaveClass("line-clamp-2");
      expect(hint).not.toHaveClass("truncate");
    });

    it("ENT-03: the hint is in Portuguese when the app is", () => {
      touch();
      useLiveStore.setState({ locale: "pt-BR" });
      mockApi({});
      renderComposer();
      expect(screen.getByText("Enter quebra a linha · toque em Enviar")).toBeInTheDocument();
      expect(screen.queryByText("Enter envia · Shift+Enter quebra a linha")).toBeNull();
    });

    it("ENT-04: the on-screen keyboard's Return key is a new-line key", () => {
      touch();
      mockApi({});
      renderComposer();
      expect(screen.getByRole("textbox", { name: "Message deploys" })).toHaveAttribute(
        "enterkeyhint",
        "enter",
      );
    });

    it("ENT-05: with the mention list open Enter still completes the mention and sends nothing", async () => {
      touch();
      const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
      const user = userEvent.setup();
      const atlas = makeMember({
        kind: "agent",
        member_id: "a1",
        name: "Atlas",
        intelligence: makeAI(),
      });
      renderComposer("post", [atlas]);
      const box = screen.getByRole("textbox", { name: "Message deploys" });
      await user.type(box, "@at{Enter}");
      expect(box).toHaveValue("@atlas ");
      expect(posts(calls)).toHaveLength(0);
    });
  });

  describe.each([
    ["a mouse or a trackpad", () => stubPointer("fine")],
    [
      "a touch laptop whose main pointer is its trackpad",
      () =>
        stubMatchMedia((query) =>
          ["(any-pointer: coarse)", "(pointer: fine)", "(hover: hover)"].includes(query),
        ),
    ],
  ] satisfies [string, () => void][])("with %s", (_name, desktop) => {
    it("ENT-02: Enter sends the trimmed text and Shift+Enter adds a new line", async () => {
      desktop();
      const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
      const user = userEvent.setup();
      renderComposer();
      const box = screen.getByRole("textbox", { name: "Message deploys" });

      await user.type(box, "line one{Shift>}{Enter}{/Shift}line two");
      expect(box).toHaveValue("line one\nline two");
      expect(posts(calls)).toHaveLength(0);

      await user.type(box, "  {Enter}");
      await waitFor(() => expect(posts(calls)).toHaveLength(1));
      expect(posts(calls)[0]?.body).toEqual({ text: "line one\nline two" });
      await waitFor(() => expect(box).toHaveValue(""));
    });

    it("ENT-03: the hint says Enter sends and Shift+Enter adds a line", () => {
      desktop();
      mockApi({});
      renderComposer();
      expect(screen.getByText(sendsOnEnter)).toBeInTheDocument();
      expect(screen.queryByText(newLineOnEnter)).toBeNull();
    });

    it("ENT-04: the on-screen keyboard's Return key is a send key", () => {
      desktop();
      mockApi({});
      renderComposer();
      expect(screen.getByRole("textbox", { name: "Message deploys" })).toHaveAttribute(
        "enterkeyhint",
        "send",
      );
    });
  });
});

describe("the keyboard stays open while writing (KBD-03)", () => {
  it("keeps focus in the text box when Send is pressed", async () => {
    // The answer is held back, so what is checked is the press itself, not the refocus after it.
    let answer: (response: Response) => void = () => {};
    mockApi({
      "POST /rooms/r1/messages": () =>
        new Promise<Response>((resolve) => {
          answer = resolve;
        }),
    });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "on my way");
    expect(box).toHaveFocus();

    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(box).toHaveFocus();

    answer(json(201, makeMessage({ seq: 9 })));
    await waitFor(() => expect(box).toHaveValue(""));
    expect(box).toHaveFocus();
  });

  it("does not let a press on Send, with a finger or a mouse, take focus from the text box", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.type(screen.getByRole("textbox", { name: "Message deploys" }), "hi");
    const send = screen.getByRole("button", { name: "Send" });
    expect(send).toBeEnabled();
    // A press that is cancelled moves no focus: the on-screen keyboard stays where it is.
    expect(fireEvent.pointerDown(send, { pointerType: "touch" })).toBe(false);
    expect(fireEvent.pointerDown(send, { pointerType: "mouse" })).toBe(false);
    expect(fireEvent.mouseDown(send)).toBe(false);
  });

  it("still sends on a tap and on the keyboard: only the press is cancelled, not the click", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "first");
    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    await waitFor(() => expect(box).toHaveValue(""));

    await user.type(box, "second");
    screen.getByRole("button", { name: "Send" }).focus();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(posts(calls)).toHaveLength(2));
    // A message sent from the Send button itself still hands focus back to the text box.
    await waitFor(() => expect(box).toHaveFocus());
  });

  it("keeps the files and the focus when a send fails: nothing is lost to retry", async () => {
    mockApi({ "POST /rooms/r1/messages": apiError(413, "too_large") });
    const user = userEvent.setup();
    renderComposer();
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "see attached");
    await user.upload(
      screen.getByLabelText("Attach files"),
      new File(["x"], "big.log", { type: "text/plain" }),
    );
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/too large/i);
    expect(screen.getByText("big.log")).toBeInTheDocument();
    expect(box).toHaveValue("see attached");
  });
});

describe("editing a message in the composer (EDT-10)", () => {
  const original = makeMessage({ seq: 4, text: "helo wrld" });
  const patchPath = "/rooms/r1/messages/m-r1-4";
  const patches = <T extends { method: string; path: string }>(calls: T[]) =>
    calls.filter((c) => c.method === "PATCH" && c.path === patchPath);
  const saved = (over: Partial<Message> = {}) =>
    json(200, { ...original, text: "hello world", edited_at: "2026-10-19T13:00:00Z", ...over });

  function renderEditing(message: Message | null = original, members?: RoomMember[]) {
    const onEditDone = vi.fn();
    const queryClient = makeQueryClient();
    const element = (editing: Message | null) => (
      <AppProviders queryClient={queryClient}>
        <Composer
          roomId="r1"
          roomName="deploys"
          level="post"
          members={members}
          editing={editing}
          onEditDone={onEditDone}
        />
      </AppProviders>
    );
    const utils = render(element(message));
    return {
      ...utils,
      onEditDone,
      queryClient,
      editing: (m: Message | null) => utils.rerender(element(m)),
    };
  }

  const box = () => screen.getByRole("textbox", { name: "Message deploys" });

  it("a file dropped while editing is not left to the browser and attaches nothing", () => {
    mockApi({});
    renderEditing();
    const zone = screen.getByTestId("composer-dropzone");
    const drag = withFiles(fileOf("shot.png", "image/png", 2048));

    expect(fireEvent.dragOver(zone, { dataTransfer: drag })).toBe(false);
    expect(fireEvent.drop(zone, { dataTransfer: drag })).toBe(false);

    expect(screen.queryByRole("list", { name: "Attachments" })).toBeNull();
    expect(zone).not.toHaveAttribute("data-dragging");
  });

  it("loads the text under an 'Editing message' bar, without the attach button, and saves instead of sending", () => {
    mockApi({});
    renderEditing();
    expect(box()).toHaveValue("helo wrld");
    expect(screen.getByText("Editing message")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancel editing" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Attach files")).toBeNull();
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    expect(screen.getByRole("button", { name: "Save changes" })).toBeInTheDocument();
    expect(screen.getByText("Enter to save · Esc to cancel")).toBeInTheDocument();
  });

  it("puts the caret at the end of the text, ready to type", async () => {
    mockApi({});
    renderEditing();
    await waitFor(() => expect(box()).toHaveFocus());
    expect((box() as HTMLTextAreaElement).selectionStart).toBe("helo wrld".length);
  });

  it("Enter saves the trimmed new text with PATCH and leaves editing; nothing is posted", async () => {
    const { calls } = mockApi({ [`PATCH ${patchPath}`]: saved() });
    const user = userEvent.setup();
    const { onEditDone } = renderEditing();
    await user.clear(box());
    await user.type(box(), "  hello world  {Enter}");

    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ text: "hello world" });
    await waitFor(() => expect(onEditDone).toHaveBeenCalledTimes(1));
    expect(posts(calls)).toHaveLength(0);
  });

  it("the Save button saves too, and the answer replaces the message in the timeline", async () => {
    const { calls } = mockApi({ [`PATCH ${patchPath}`]: saved() });
    const user = userEvent.setup();
    const { onEditDone, queryClient } = renderEditing();
    queryClient.setQueryData<MessagePages>(queryKeys.roomMessages("r1"), {
      pages: [{ items: [original], has_more: false }],
      pageParams: [undefined],
    });
    await user.type(box(), "!");
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    await waitFor(() => expect(onEditDone).toHaveBeenCalled());
    const items = queryClient.getQueryData<MessagePages>(queryKeys.roomMessages("r1"))?.pages[0]
      ?.items;
    expect(items?.[0]).toMatchObject({
      seq: 4,
      text: "hello world",
      edited_at: "2026-10-19T13:00:00Z",
    });
  });

  it("Escape leaves editing without sending anything and the Cancel button does the same", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    const { onEditDone } = renderEditing();
    await user.type(box(), " and more");
    await user.keyboard("{Escape}");
    expect(onEditDone).toHaveBeenCalledTimes(1);

    await user.click(screen.getByRole("button", { name: "Cancel editing" }));
    expect(onEditDone).toHaveBeenCalledTimes(2);
    expect(calls.filter((c) => c.method !== "GET")).toHaveLength(0);
  });

  it("Save waits for a change: the same text, and a blank one, cannot be saved", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderEditing();
    const save = screen.getByRole("button", { name: "Save changes" });
    expect(save).toBeDisabled();
    await user.keyboard("{Enter}");
    expect(patches(calls)).toHaveLength(0);

    await user.type(box(), "x");
    expect(save).toBeEnabled();
    await user.keyboard("{Backspace}");
    expect(save).toBeDisabled();

    await user.clear(box());
    expect(save).toBeDisabled();
    await user.type(box(), "   ");
    expect(save).toBeDisabled();
  });

  it("an unchanged edit is not saved, even when the stored text has surrounding whitespace", async () => {
    const cushioned = makeMessage({ seq: 4, text: "hello\n" });
    const { calls } = mockApi({ [`PATCH ${patchPath}`]: saved({ text: "hello\n" }) });
    const user = userEvent.setup();
    const { onEditDone } = renderEditing(cushioned);
    expect(box()).toHaveValue("hello\n");
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();

    // Enter without typing anything: the stored newline is not a change to save.
    await user.keyboard("{Enter}");
    expect(patches(calls)).toHaveLength(0);
    expect(onEditDone).not.toHaveBeenCalled();
    expect(screen.getByText("Editing message")).toBeInTheDocument();

    // One typed character is a change, and saves what the box holds.
    await user.type(box(), "!");
    await user.keyboard("{Enter}");
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ text: "hello\n!" });
    await waitFor(() => expect(onEditDone).toHaveBeenCalledTimes(1));
  });

  it("a message with files may lose its text: the files stay", async () => {
    const withFile = makeMessage({
      seq: 4,
      text: "caption",
      attachments: [{ id: "f1", name: "a.txt", mime: "text/plain", size: 3, status: "stored" }],
    });
    const { calls } = mockApi({ [`PATCH ${patchPath}`]: saved({ text: "" }) });
    const user = userEvent.setup();
    const { onEditDone } = renderEditing(withFile);
    expect(screen.getByText("a.txt")).toBeInTheDocument();
    await user.clear(box());
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
    expect(patches(calls)[0]?.body).toEqual({ text: "" });
    await waitFor(() => expect(onEditDone).toHaveBeenCalled());
  });

  it("Shift+Enter adds a line; over MaxTextLength cannot be saved", async () => {
    const { calls } = mockApi({});
    const user = userEvent.setup();
    renderEditing();
    await user.type(box(), "{Shift>}{Enter}{/Shift}second line");
    expect(box()).toHaveValue("helo wrld\nsecond line");
    expect(patches(calls)).toHaveLength(0);

    await user.clear(box());
    await user.paste("x".repeat(MaxTextLength + 1));
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(screen.getByTestId("composer-counter")).toHaveTextContent("16,001 / 16,000");
  });

  it("on a touch device Enter adds a line and the Save button saves", async () => {
    stubPointer("coarse");
    const { calls } = mockApi({ [`PATCH ${patchPath}`]: saved() });
    const user = userEvent.setup();
    renderEditing();
    expect(screen.getByText("Enter for a new line · tap Save to save")).toBeInTheDocument();
    await user.type(box(), "{Enter}more");
    expect(patches(calls)).toHaveLength(0);
    expect(box()).toHaveValue("helo wrld\nmore");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(patches(calls)).toHaveLength(1));
  });

  it.each([
    [409, "conflict", "This message was deleted, so it can’t be edited."],
    [403, "forbidden", "You can only edit your own messages."],
    [
      403,
      "counterpart_disabled",
      "The other participant of this conversation is disabled, so its messages can’t be edited.",
    ],
    [422, "invalid", "The message could not be saved."],
    [500, "internal", "Something went wrong. Try again."],
  ])("keeps the box and says why when the server answers %i", async (status, code, text) => {
    mockApi({ [`PATCH ${patchPath}`]: apiError(status, code, "The message could not be saved.") });
    const user = userEvent.setup();
    const { onEditDone } = renderEditing();
    await user.type(box(), "!");
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("alert")).toHaveTextContent(text);
    expect(box()).toHaveValue("helo wrld!");
    expect(onEditDone).not.toHaveBeenCalled();
  });

  it("brings back what the box held before, when editing ends", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { editing } = renderEditing(null);
    await user.type(box(), "my unfinished draft");

    editing(original);
    expect(box()).toHaveValue("helo wrld");
    editing(null);
    expect(box()).toHaveValue("my unfinished draft");
    expect(screen.queryByText("Editing message")).toBeNull();
    expect(screen.getByLabelText("Attach files")).toBeInTheDocument();
  });

  it("keeps what was typed in an edit per message, apart from the new-message draft (WA-07)", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { editing } = renderEditing(null);
    await user.type(box(), "new draft");

    editing(original);
    await user.type(box(), "!");
    expect(box()).toHaveValue("helo wrld!");
    // Back to writing a new message: its draft was never touched by the edit.
    editing(null);
    expect(box()).toHaveValue("new draft");
    // The edit is still there for its message.
    editing(original);
    expect(box()).toHaveValue("helo wrld!");
    // Cancel drops it: the next edit of the message starts from its text again.
    await user.click(screen.getByRole("button", { name: "Cancel editing" }));
    editing(null);
    editing(original);
    expect(box()).toHaveValue("helo wrld");
  });

  it("starts from another message's text when another one is chosen, and does not reset on a refreshed copy", async () => {
    mockApi({});
    const user = userEvent.setup();
    const { editing } = renderEditing(original);
    await user.type(box(), " and typed more");
    // The same message, refreshed by a live update: the person's typing stays.
    editing({ ...original, edited_at: "2026-10-19T13:00:00Z" });
    expect(box()).toHaveValue("helo wrld and typed more");
    editing(makeMessage({ seq: 6, text: "another one" }));
    expect(box()).toHaveValue("another one");
  });

  it("closes the mention list first when Escape is pressed with it open", async () => {
    mockApi({});
    const user = userEvent.setup();
    const atlas = makeMember({
      kind: "agent",
      member_id: "a1",
      name: "Atlas",
      intelligence: makeAI(),
    });
    const { onEditDone } = renderEditing(makeMessage({ seq: 4, text: "ping " }), [atlas]);
    await user.type(box(), "@at");
    expect(await screen.findByRole("listbox")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(onEditDone).not.toHaveBeenCalled();
    await user.keyboard("{Escape}");
    expect(onEditDone).toHaveBeenCalledTimes(1);
  });

  it("says 'Editando mensagem' in Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({});
    renderEditing();
    expect(screen.getByText("Editando mensagem")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Cancelar edição" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Salvar alterações" })).toBeInTheDocument();
  });
});

describe("pasting and dropping files (UI-07)", () => {
  const createObjectURL = vi.fn(() => "blob:preview-1");
  const revokeObjectURL = vi.fn();
  const realCreateObjectURL = URL.createObjectURL;
  const realRevokeObjectURL = URL.revokeObjectURL;

  beforeEach(() => {
    createObjectURL.mockClear();
    revokeObjectURL.mockClear();
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
  });
  afterAll(() => {
    URL.createObjectURL = realCreateObjectURL;
    URL.revokeObjectURL = realRevokeObjectURL;
  });

  const box = () => screen.getByRole("textbox", { name: "Message deploys" });
  const zone = () => screen.getByTestId("composer-dropzone");
  const png = () => fileOf("shot.png", "image/png", 2048);

  it("attaches a pasted image (with its thumbnail) and sends it like a picked file", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();

    fireEvent.paste(box(), { clipboardData: withFiles(png()) });

    const preview = screen.getByRole("group", { name: "shot.png, 2 KB" });
    expect(preview.querySelector("img")).toHaveAttribute("src", "blob:preview-1");
    expect(screen.getByRole("status")).toHaveTextContent("1 file attached");
    expect(box()).toHaveValue("");

    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const form = posts(calls)[0]?.body as FormData;
    expect(JSON.parse(String(form.get("message")))).toEqual({ text: "" });
    expect((form.get("file") as File).name).toBe("shot.png");
    // Sending the file takes its thumbnail with it.
    await waitFor(() => expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview-1"));
  });

  it("keeps plain text paste working, with no attachment", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();

    await user.click(box());
    await user.paste("plain words");

    expect(box()).toHaveValue("plain words");
    expect(screen.queryByRole("list", { name: "Attachments" })).toBeNull();
    expect(screen.getByRole("status")).toBeEmptyDOMElement();
  });

  it("attaches the file and not the text when the clipboard holds both", () => {
    mockApi({});
    renderComposer();
    const clipboard = withFiles(png());
    clipboard.setData("text/plain", "https://example.com/shot.png");

    // The paste's default (typing the text) is stopped by the composer.
    expect(fireEvent.paste(box(), { clipboardData: clipboard })).toBe(false);

    expect(box()).toHaveValue("");
    expect(screen.getByRole("group", { name: "shot.png, 2 KB" })).toBeInTheDocument();
  });

  it("attaches two dropped files and announces them", () => {
    mockApi({});
    renderComposer();
    const drag = withFiles(png(), fileOf("notes.pdf", "application/pdf"));

    fireEvent.dragEnter(zone(), { dataTransfer: drag });
    expect(zone()).toHaveAttribute("data-dragging", "true");
    expect(screen.getByTestId("composer-drop-hint")).toHaveTextContent("Drop files to attach");

    fireEvent.drop(zone(), { dataTransfer: drag });

    expect(zone()).not.toHaveAttribute("data-dragging");
    expect(screen.queryByTestId("composer-drop-hint")).toBeNull();
    expect(screen.getByRole("group", { name: "shot.png, 2 KB" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "notes.pdf, 120 KB" })).toBeInTheDocument();
    expect(
      within(screen.getByRole("list", { name: "Attachments" })).getAllByRole("listitem"),
    ).toHaveLength(2);
    expect(screen.getByRole("status")).toHaveTextContent("2 files attached");
  });

  it("clears the drop highlight when the files leave without being dropped", () => {
    mockApi({});
    renderComposer();
    const drag = withFiles(png());

    fireEvent.dragEnter(zone(), { dataTransfer: drag });
    expect(screen.getByTestId("composer-drop-hint")).toBeInTheDocument();
    fireEvent.dragLeave(zone(), { dataTransfer: drag });

    expect(zone()).not.toHaveAttribute("data-dragging");
    expect(screen.queryByTestId("composer-drop-hint")).toBeNull();
  });

  it("clears the highlight after enter, leave, enter, leave in one tick", () => {
    mockApi({});
    renderComposer();
    const drag = withFiles(png());

    act(() => {
      fireEvent.dragEnter(zone(), { dataTransfer: drag });
      fireEvent.dragLeave(zone(), { dataTransfer: drag });
      fireEvent.dragEnter(zone(), { dataTransfer: drag });
      fireEvent.dragLeave(zone(), { dataTransfer: drag });
    });

    expect(zone()).not.toHaveAttribute("data-dragging");
    expect(screen.queryByTestId("composer-drop-hint")).toBeNull();
  });

  it("shows two previews for two identical files and removing one keeps the other", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    renderComposer();
    const twin = () =>
      new File([new Uint8Array(10)], "same.txt", { type: "text/plain", lastModified: 1 });

    fireEvent.paste(box(), { clipboardData: withFiles(twin(), twin()) });
    const list = screen.getByRole("list", { name: "Attachments" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(errors.mock.calls.filter((c) => String(c[0]).includes("same key"))).toEqual([]);
    errors.mockRestore();

    await user.click(screen.getAllByRole("button", { name: "Remove same.txt" })[0] as HTMLElement);
    expect(within(list).getAllByRole("listitem")).toHaveLength(1);

    await user.click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    const form = posts(calls)[0]?.body as FormData;
    expect(form.getAll("file")).toHaveLength(1);
  });

  it("revokes an image's object URL when its preview is removed", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    fireEvent.paste(box(), { clipboardData: withFiles(png()) });

    await user.click(screen.getByRole("button", { name: "Remove shot.png" }));

    expect(screen.queryByRole("group", { name: "shot.png, 2 KB" })).toBeNull();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview-1");
  });

  it("shows a non-image file's name and size, and no image", () => {
    mockApi({});
    renderComposer();
    fireEvent.paste(box(), {
      clipboardData: withFiles(fileOf("notes.pdf", "application/pdf")),
    });

    const preview = screen.getByRole("group", { name: "notes.pdf, 120 KB" });
    expect(preview).toHaveTextContent("notes.pdf");
    expect(preview).toHaveTextContent("120 KB");
    expect(preview.querySelector("img")).toBeNull();
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it("tells a screen reader that one file is attached, in Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mockApi({});
    renderComposer();
    fireEvent.paste(screen.getByRole("textbox", { name: "Mensagem para deploys" }), {
      clipboardData: withFiles(png()),
    });

    expect(screen.getByRole("status")).toHaveTextContent("1 arquivo anexado");
    expect(screen.getByRole("list", { name: "Anexos" })).toBeInTheDocument();
  });
});
