import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "@/components/composer";
import { type Level, MaxTextLength, type RoomMember } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeMember, makeMessage } from "@/test/fixtures";
import { stubMatchMedia, stubPointer } from "@/test/media";
import { renderWithProviders } from "@/test/render";

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
