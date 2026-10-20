import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "@/components/composer";
import { type Level, MaxTextLength, type RoomMember } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMessage } from "@/test/fixtures";
import { renderWithProviders } from "@/test/render";

const chats: RoomMember[] = [
  {
    id: "mb-ops",
    kind: "chat",
    member_id: "c1",
    name: "Ops on call",
    outbound: true,
    connector_kind: "telegram",
    chat_kind: "group",
    display_name: "",
    sign_outbound: true,
    delivery: "all",
    keywords: [],
    created_at: "2026-10-01T12:00:00Z",
  },
  {
    id: "mb-dev",
    kind: "chat",
    member_id: "c2",
    name: "#deploys",
    outbound: false,
    connector_kind: "discord",
    chat_kind: "channel",
    display_name: "",
    sign_outbound: true,
    delivery: "all",
    keywords: [],
    created_at: "2026-10-01T12:00:00Z",
  },
];

function renderComposer(level: Level = "post") {
  return renderWithProviders(
    <Composer roomId="r1" roomName="deploys" level={level} members={chats} />,
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

  it("sends to chosen chats only when recipients are picked", async () => {
    const { calls } = mockApi({ "POST /rooms/r1/messages": json(201, makeMessage({ seq: 9 })) });
    const user = userEvent.setup();
    renderComposer();
    const to = screen.getByRole("button", { name: /To: chats with outbound on/ });
    await user.click(to);
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("checkbox", { name: /Ops on call/ }));
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: /To: 1 chat/ })).toBeInTheDocument();
    await user.type(
      screen.getByRole("textbox", { name: "Message deploys" }),
      "only telegram{Enter}",
    );
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "only telegram", recipients: ["c1"] });
  });

  it("does not let a listen-only chat (outbound off) be picked as a recipient (DLV-05)", async () => {
    mockApi({});
    const user = userEvent.setup();
    renderComposer();
    await user.click(screen.getByRole("button", { name: /To: chats with outbound on/ }));
    const dialog = await screen.findByRole("dialog");
    const listenOnly = within(dialog).getByRole("checkbox", { name: /#deploys/ });
    expect(listenOnly).toBeDisabled();
    expect(listenOnly).toHaveAccessibleName(/listen-only/);
    expect(within(dialog).getByRole("checkbox", { name: /Ops on call/ })).toBeEnabled();
    await user.click(listenOnly);
    expect(listenOnly).not.toBeChecked();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: /To: chats with outbound on/ })).toBeInTheDocument();
  });

  it("explains a 422 listen-only recipient on the recipients picker and keeps the text", async () => {
    mockApi({
      "POST /rooms/r1/messages": apiError(422, "invalid", "validation failed", {
        recipients: "listen-only",
      }),
    });
    const user = userEvent.setup();
    renderComposer();
    await user.click(screen.getByRole("button", { name: /To: chats with outbound on/ }));
    const dialog = await screen.findByRole("dialog");
    await user.click(within(dialog).getByRole("checkbox", { name: /Ops on call/ }));
    await user.keyboard("{Escape}");
    const box = screen.getByRole("textbox", { name: "Message deploys" });
    await user.type(box, "hello{Enter}");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/listen-only/);
    expect(alert).not.toHaveTextContent("validation failed");
    const to = screen.getByRole("button", { name: /To: 1 chat/ });
    expect(to).toHaveAttribute("aria-invalid", "true");
    expect(to).toHaveAccessibleDescription(alert.textContent ?? "");
    expect(box).toHaveValue("hello");
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
