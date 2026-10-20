import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CreatedToken } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAgent, makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
const SECRET = "pl_agent_synthetic0123456789";

const created: CreatedToken = {
  token: {
    id: "t1",
    kind: "agent",
    name: "build-bot",
    prefix: "pl_agent_syn",
    created_at: "2026-10-19T12:00:00Z",
  },
  secret: SECRET,
};

beforeEach(() => useLiveStore.setState({ locale: "en", presence: {} }));
afterEach(() => vi.unstubAllGlobals());

/** Text of a labelled code block (CodeBlock renders a figure captioned with its label). */
const code = (scope: HTMLElement, name: string) =>
  within(scope).getByRole("figure", { name }).querySelector("pre")?.textContent ?? "";

const row = (name: string) => screen.getByRole("listitem", { name });

describe("agents page (UI-15)", () => {
  it("lists agents with live state and last seen time, online first", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, {
        items: [
          makeAgent({ id: "a1", name: "reviewer", online: false, last_seen_at: undefined }),
          makeAgent({ id: "a2", name: "release-bot", online: true }),
          makeAgent({
            id: "a3",
            name: "docs-writer",
            online: false,
            last_seen_at: "2026-10-18T12:00:00Z",
          }),
        ],
      }),
    });
    renderRoute("/agents");

    const list = await screen.findByRole("list", { name: "Your agents" });
    const items = within(list).getAllByRole("listitem");
    expect(items.map((i) => i.getAttribute("aria-label"))).toEqual([
      "release-bot",
      "docs-writer",
      "reviewer",
    ]);
    expect(within(row("release-bot")).getByText("Online")).toBeInTheDocument();
    expect(within(row("docs-writer")).getByText("Offline")).toBeInTheDocument();
    expect(within(row("docs-writer")).getByText(/^Last seen /)).toBeInTheDocument();
    expect(within(row("reviewer")).getByText("Never connected")).toBeInTheDocument();
  });

  it("flips the row's status dot when a presence event arrives", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [makeAgent({ id: "a1", name: "release-bot" })] }),
    });
    const { queryClient } = renderRoute("/agents");
    await screen.findByRole("listitem", { name: "release-bot" });
    expect(within(row("release-bot")).getByText("Offline")).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: true, last_seen_at: "2026-10-19T12:05:00Z" },
      }),
    );
    expect(within(row("release-bot")).getByText("Online")).toBeInTheDocument();
    expect(within(row("release-bot")).queryByText("Offline")).toBeNull();
  });

  it("shows sharing and delete only on agents the user manages", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, {
        items: [
          makeAgent({ id: "a1", name: "mine" }),
          makeAgent({
            id: "a2",
            name: "theirs",
            my_level: "use",
            owner: { id: "u2", username: "bruno", display_name: "Bruno" },
          }),
        ],
      }),
    });
    renderRoute("/agents");
    await screen.findByRole("listitem", { name: "mine" });
    expect(within(row("mine")).getByRole("button", { name: "Share mine" })).toBeInTheDocument();
    expect(within(row("mine")).getByRole("button", { name: "Delete mine" })).toBeInTheDocument();
    expect(within(row("theirs")).queryByRole("button", { name: /Share|Delete/ })).toBeNull();
    expect(within(row("theirs")).getByText("by @bruno")).toBeInTheDocument();
  });

  it("deletes an agent after confirmation", async () => {
    let agents = [makeAgent({ id: "a1", name: "release-bot" })];
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": () => json(200, { items: agents }),
      "DELETE /agents/a1": () => {
        agents = [];
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/agents");
    await user.click(await screen.findByRole("button", { name: "Delete release-bot" }));
    // Destructive confirmations are alert dialogs, like every other one in the app.
    const dialog = await screen.findByRole("alertdialog", { name: "Delete release-bot?" });
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "Delete agent" }));
    await waitFor(() => expect(screen.queryByRole("listitem", { name: "release-bot" })).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual(["/agents/a1"]);
  });

  it("shows an empty state that starts the connect wizard", async () => {
    mockApi({ "GET /me": json(200, me), "GET /agents": json(200, { items: [] }) });
    const user = userEvent.setup();
    renderRoute("/agents");
    expect(await screen.findByRole("heading", { name: "No agents yet" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Connect an agent" }));
    expect(await screen.findByRole("dialog", { name: "Connect an agent" })).toBeInTheDocument();
  });
});

describe("connect an agent (UI-14)", () => {
  async function openWizard(routes: Parameters<typeof mockApi>[0] = {}) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [] }),
      "POST /tokens": json(201, created),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/agents");
    await user.click(await screen.findByRole("button", { name: "Connect an agent" }));
    const dialog = await screen.findByRole("dialog", { name: "Connect an agent" });
    return { ...api, ...utils, user, dialog };
  }

  it("creates an agent token and shows the .mcp.json and launch command", async () => {
    const { user, dialog, calls } = await openWizard();
    await user.type(within(dialog).getByLabelText("Agent name"), "build-bot");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));

    await within(dialog).findByRole("figure", { name: ".mcp.json" });
    expect(calls.find((c) => c.method === "POST" && c.path === "/tokens")?.body).toEqual({
      kind: "agent",
      name: "build-bot",
    });
    const server = JSON.parse(code(dialog, ".mcp.json")).mcpServers.partyline;
    expect(server.command).toBe("partyline");
    expect(server.args).toEqual(["channel"]);
    expect(server.env).toEqual({
      PARTYLINE_URL: window.location.origin,
      PARTYLINE_TOKEN: SECRET,
      PARTYLINE_AGENT: "build-bot",
    });
    expect(code(dialog, "Launch command")).toBe(
      "claude --dangerously-load-development-channels server:partyline",
    );
    expect(within(dialog).getByText(/shown only now/i)).toBeInTheDocument();
    expect(within(dialog).getByText(/confirmation screen/i)).toBeInTheDocument();
    expect(within(dialog).getByRole("link", { name: /Claude Code guide/ })).toHaveAttribute(
      "href",
      "https://github.com/wnexous/partyline/blob/main/docs/claude-code.md",
    );
    expect(code(dialog, "Install command")).toBe(
      "go install github.com/wnexous/partyline/cmd/partyline@latest",
    );
    expect(within(dialog).getByRole("link", { name: /GitHub Releases/ })).toHaveAttribute(
      "href",
      "https://github.com/wnexous/partyline/releases",
    );
  });

  it("copies the config with the token to the clipboard", async () => {
    const { user, dialog } = await openWizard();
    await user.type(within(dialog).getByLabelText("Agent name"), "build-bot");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    await within(dialog).findByRole("figure", { name: ".mcp.json" });
    await user.click(within(dialog).getByRole("button", { name: "Copy .mcp.json" }));
    const copied = await navigator.clipboard.readText();
    expect(JSON.parse(copied).mcpServers.partyline.env.PARTYLINE_TOKEN).toBe(SECRET);
  });

  it("rejects agent names the channel would not keep", async () => {
    const { user, dialog, calls } = await openWizard();
    await user.type(within(dialog).getByLabelText("Agent name"), "Build Bot");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText(/Use a-z, 0-9, dot, _ or -/)).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("shows a 422 reason under the name", async () => {
    const { user, dialog } = await openWizard({
      "POST /tokens": apiError(422, "invalid", "validation failed", {
        name: "must have 1 to 64 characters",
      }),
    });
    await user.type(within(dialog).getByLabelText("Agent name"), "build-bot");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText("must have 1 to 64 characters")).toBeInTheDocument();
  });

  it("turns to connected when the new agent comes online", async () => {
    let agents: ReturnType<typeof makeAgent>[] = [];
    const { user, dialog, queryClient } = await openWizard({
      "GET /agents": () => json(200, { items: agents }),
    });
    await user.type(within(dialog).getByLabelText("Agent name"), "build-bot");
    await user.click(within(dialog).getByRole("button", { name: "Create token" }));
    expect(await within(dialog).findByText(/Waiting for build-bot/)).toBeInTheDocument();

    agents = [makeAgent({ id: "a7", name: "build-bot", online: true })];
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a7", online: true },
      }),
    );
    expect(await within(dialog).findByText("build-bot is online")).toBeInTheDocument();
  });
});
