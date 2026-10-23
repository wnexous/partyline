import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Composer } from "@/components/composer";
import { type Level, MaxTextLength, type RoomMember } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMember, makeMessage } from "@/test/fixtures";
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
