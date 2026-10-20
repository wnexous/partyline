import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeMe } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();
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

  it("shows the last seen time relative to now and follows presence events", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    try {
      mockApi({
        "GET /me": json(200, me),
        "GET /agents": json(200, {
          items: [
            makeAgent({
              id: "a3",
              name: "docs-writer",
              online: false,
              last_seen_at: "2026-10-19T09:00:00Z",
            }),
          ],
        }),
      });
      const { queryClient } = renderRoute("/agents");
      await screen.findByRole("listitem", { name: "docs-writer" });
      expect(within(row("docs-writer")).getByText("Last seen 3 hr. ago")).toBeInTheDocument();

      act(() =>
        handleLiveEvent(queryClient, useLiveStore.getState(), {
          type: "agent.presence",
          data: { agent_id: "a3", online: false, last_seen_at: "2026-10-19T11:55:00Z" },
        }),
      );
      expect(within(row("docs-writer")).getByText("Last seen 5 min. ago")).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
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

describe("connect an agent (WIZ-01)", () => {
  const harnesses = [
    { tab: "Claude Code", start: "partyline claude" },
    { tab: "OpenCode", start: "opencode" },
    { tab: "pi", start: "pi" },
    { tab: "omp", start: "omp" },
    { tab: "Codex", start: "codex" },
    { tab: "Gemini", start: "gemini" },
  ];
  const PUSH = "Room messages arrive in the running session as they come in.";
  const TURN_END =
    "Messages arrive when it finishes its current turn, or when it calls wait_for_messages.";

  async function openWizard(routes: Parameters<typeof mockApi>[0] = {}) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [] }),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/agents");
    await user.click(await screen.findByRole("button", { name: "Connect an agent" }));
    const dialog = await screen.findByRole("dialog", { name: "Connect an agent" });
    return { ...api, ...utils, user, dialog };
  }

  it("shows the install command for this instance and the browser setup command", async () => {
    const { dialog, calls } = await openWizard();
    expect(code(dialog, "Install command")).toBe(
      `curl -fsSL ${window.location.origin}/install.sh | sh`,
    );
    expect(code(dialog, "Setup command")).toBe("partyline setup --browser");
    expect(within(dialog).getByRole("link", { name: /GitHub Releases/ })).toHaveAttribute(
      "href",
      "https://github.com/wnexous/partyline/releases",
    );
    // setup handles the credentials: the wizard creates no token and warns about none.
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(within(dialog).queryByText(/shown only now/i)).toBeNull();
    expect(within(dialog).queryByLabelText("Agent name")).toBeNull();
  });

  it("shows each harness's start command and how messages reach it", async () => {
    const { user, dialog } = await openWizard();
    const tabs = within(dialog).getByRole("tablist", { name: "Harness" });
    expect(
      within(tabs)
        .getAllByRole("tab")
        .map((tab) => tab.textContent),
    ).toEqual(harnesses.map((h) => h.tab));
    expect(within(tabs).getByRole("tab", { name: "Claude Code" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    for (const h of harnesses) {
      await user.click(within(tabs).getByRole("tab", { name: h.tab }));
      const panel = within(dialog).getByRole("tabpanel");
      expect(code(panel, "Start command")).toBe(h.start);
      const turnEnd = h.tab === "Codex" || h.tab === "Gemini";
      expect(within(panel).getByText(turnEnd ? TURN_END : PUSH)).toBeInTheDocument();
      expect(within(panel).queryByText(turnEnd ? PUSH : TURN_END)).toBeNull();
    }
  });

  it("explains Claude Code's channel confirmation with a link to its guide", async () => {
    const { dialog } = await openWizard();
    const panel = within(dialog).getByRole("tabpanel");
    expect(within(panel).getByText(/confirmation screen/i)).toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: /Claude Code guide/ })).toHaveAttribute(
      "href",
      "https://github.com/wnexous/partyline/blob/main/docs/claude-code.md",
    );
  });

  it("keeps a manual entry per harness under Advanced, reading the token from the config file", async () => {
    const { user, dialog } = await openWizard();
    await user.click(within(dialog).getByText("Advanced: register by hand"));
    expect(within(dialog).getByText("~/.config/partyline/config.json")).toBeInTheDocument();

    const claude = JSON.parse(code(dialog, ".mcp.json"));
    expect(claude).toEqual({
      mcpServers: { partyline: { command: "partyline", args: ["channel"] } },
    });

    const tabs = within(dialog).getByRole("tablist", { name: "Harness" });
    await user.click(within(tabs).getByRole("tab", { name: "OpenCode" }));
    expect(JSON.parse(code(dialog, "~/.config/opencode/opencode.json"))).toEqual({
      mcp: {
        partyline: {
          type: "local",
          command: ["partyline", "channel", "--tools-only"],
          enabled: true,
        },
      },
    });
    await user.click(within(tabs).getByRole("tab", { name: "pi" }));
    expect(JSON.parse(code(dialog, "~/.pi/agent/mcp.json"))).toEqual({
      mcpServers: { partyline: { command: "partyline", args: ["channel", "--tools-only"] } },
    });
    await user.click(within(tabs).getByRole("tab", { name: "omp" }));
    expect(JSON.parse(code(dialog, "~/.omp/agent/mcp.json"))).toEqual({
      mcpServers: { partyline: { command: "partyline", args: ["channel"] } },
    });
    await user.click(within(tabs).getByRole("tab", { name: "Codex" }));
    expect(code(dialog, "~/.codex/config.toml")).toBe(
      '[mcp_servers.partyline]\ncommand = "partyline"\nargs = ["channel", "--tools-only"]',
    );
    await user.click(within(tabs).getByRole("tab", { name: "Gemini" }));
    const gemini = code(dialog, "~/.gemini/settings.json");
    expect(JSON.parse(gemini)).toEqual({
      mcpServers: { partyline: { command: "partyline", args: ["channel", "--tools-only"] } },
    });
    // No credential is ever inlined in a manual entry.
    for (const text of [JSON.stringify(claude), gemini]) {
      expect(text).not.toMatch(/token|PARTYLINE_|pl_agent|https?:/i);
    }
    expect(within(dialog).getByText("partyline setup --token").tagName).toBe("CODE");
    expect(within(dialog).getByRole("link", { name: "API tokens" })).toHaveAttribute(
      "href",
      "/tokens",
    );
  });

  it("names the first session that comes online while it is open", async () => {
    let agents = [makeAgent({ id: "a1", name: "old-box", online: true })];
    const { dialog, queryClient } = await openWizard({
      "GET /agents": () => json(200, { items: agents }),
    });
    expect(
      await within(dialog).findByText("Waiting for a session to connect…"),
    ).toBeInTheDocument();

    agents = [...agents, makeAgent({ id: "a7", name: "partyline-2", online: true })];
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a7", online: true },
      }),
    );
    expect(await within(dialog).findByText("partyline-2 is online")).toBeInTheDocument();
    expect(within(dialog).queryByText("old-box is online")).toBeNull();
  });
});
