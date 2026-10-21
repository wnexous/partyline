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

    // Agents of no project form the "No project" group (PROJ-04).
    const list = await screen.findByRole("list", { name: "Agents of No project" });
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
    { tab: "OpenCode", start: "partyline opencode" },
    { tab: "pi", start: "partyline pi" },
    { tab: "omp", start: "partyline omp" },
    { tab: "Codex", start: "partyline codex" },
    { tab: "Gemini", start: "partyline gemini" },
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
      "https://github.com/wnexous/partyline/blob/main/docs/agents.md#claude-code",
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
      mcpServers: { partyline: { command: "partyline", args: ["channel", "--harness", "omp"] } },
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

describe("agents by project (PROJ-04, ID-04, ID-05, PROJ-05)", () => {
  // Radix Select needs pointer capture and scrollIntoView, which jsdom lacks.
  beforeEach(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.setPointerCapture ??= () => {};
    Element.prototype.releasePointerCapture ??= () => {};
    Element.prototype.scrollIntoView ??= () => {};
  });

  const ruthe = { id: "p1", slug: "ruthe", name: "Ruthe" };
  const web = { id: "p2", slug: "web", name: "Web" };
  const alpha = { id: "p3", slug: "alpha", name: "Alpha" };
  const temp = (over: Parameters<typeof makeAgent>[0]) =>
    makeAgent({ kind: "temporary", harness: "claude", machine: "laptop", ...over });
  const fleet = () => [
    temp({ id: "w1", name: "web-claude", display_name: "Web · Claude Code", project: web }),
    temp({
      id: "r1",
      name: "ruthe-claude",
      display_name: "Ruthe · Claude Code",
      project: ruthe,
      last_seen_at: "2026-10-18T12:00:00Z",
    }),
    temp({
      id: "r2",
      name: "ruthe-codex",
      display_name: "Ruthe · Codex",
      project: ruthe,
      harness: "codex",
      machine: "desk",
      online: true,
    }),
    makeAgent({ id: "f1", name: "builder", project: alpha }),
    makeAgent({ id: "n1", name: "loose" }),
    temp({
      id: "x1",
      name: "web-claude-2",
      display_name: "Web · Claude Code #2",
      project: web,
      archived: true,
    }),
  ];
  const groupNames = () =>
    screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent ?? "");

  it("groups agents by project, live projects first, with harness, machine and kind", async () => {
    mockApi({ "GET /me": json(200, me), "GET /agents": json(200, { items: fleet() }) });
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Ruthe" });

    expect(groupNames()).toEqual(["Ruthe", "Alpha", "Web", "No project"]);
    const group = screen.getByRole("list", { name: "Agents of Ruthe" });
    expect(
      within(group)
        .getAllByRole("listitem")
        .map((i) => i.getAttribute("aria-label")),
    ).toEqual(["Ruthe · Codex", "Ruthe · Claude Code"]);
    expect(screen.getByText("1 of 2 online")).toBeInTheDocument();

    const codex = row("Ruthe · Codex");
    expect(within(codex).getByText("Online")).toBeInTheDocument();
    expect(within(codex).getByText("Codex")).toBeInTheDocument();
    expect(within(codex).getByText("desk")).toBeInTheDocument();
    expect(within(codex).getByText("ruthe-codex")).toBeInTheDocument();
    expect(within(codex).getByText("Temporary")).toBeInTheDocument();
    const fixed = row("builder");
    expect(within(fixed).queryByText("Temporary")).toBeNull();
    expect(within(fixed).getByText("Agent")).toBeInTheDocument();
    expect(within(fixed).queryByText("builder", { selector: ".font-mono" })).toBeNull();

    expect(screen.queryByRole("listitem", { name: "Web · Claude Code #2" })).toBeNull();
  });

  it("shows archived agents only behind the toggle", async () => {
    mockApi({ "GET /me": json(200, me), "GET /agents": json(200, { items: fleet() }) });
    const user = userEvent.setup();
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Web" });
    expect(screen.queryByRole("listitem", { name: "Web · Claude Code #2" })).toBeNull();

    await user.click(screen.getByRole("switch", { name: "Show archived (1)" }));
    const archived = screen.getByRole("listitem", { name: "Web · Claude Code #2" });
    expect(within(archived).getByText("Archived")).toBeInTheDocument();
  });

  it("asks the server for archived agents too", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: fleet() }),
    });
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Ruthe" });
    expect(
      calls.some(
        (c) => c.path === "/agents" && c.url.searchParams.get("include_archived") === "true",
      ),
    ).toBe(true);
  });

  it("filters by machine and by harness", async () => {
    mockApi({ "GET /me": json(200, me), "GET /agents": json(200, { items: fleet() }) });
    const user = userEvent.setup();
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Ruthe" });

    await user.click(screen.getByRole("combobox", { name: "Machine" }));
    await user.click(await screen.findByRole("option", { name: "desk" }));
    expect(groupNames()).toEqual(["Ruthe"]);
    expect(
      within(screen.getByRole("list", { name: "Agents of Ruthe" }))
        .getAllByRole("listitem")
        .map((i) => i.getAttribute("aria-label")),
    ).toEqual(["Ruthe · Codex"]);

    await user.click(screen.getByRole("combobox", { name: "Machine" }));
    await user.click(await screen.findByRole("option", { name: "All machines" }));
    await user.click(screen.getByRole("combobox", { name: "Harness" }));
    await user.click(await screen.findByRole("option", { name: "Claude Code" }));
    expect(groupNames()).toEqual(["Ruthe", "Web"]);
    expect(screen.queryByRole("listitem", { name: "Ruthe · Codex" })).toBeNull();
    expect(screen.getByRole("listitem", { name: "Ruthe · Claude Code" })).toBeInTheDocument();
  });

  it("renames an agent in place", async () => {
    let agents = fleet();
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": () => json(200, { items: agents }),
      "PATCH /agents/r2": () => {
        agents = agents.map((a) => (a.id === "r2" ? { ...a, display_name: "Ruthe reviewer" } : a));
        return json(
          200,
          agents.find((a) => a.id === "r2"),
        );
      },
    });
    const user = userEvent.setup();
    renderRoute("/agents");
    await user.click(await screen.findByRole("button", { name: "Rename Ruthe · Codex" }));
    const field = screen.getByRole("textbox", { name: "New name for Ruthe · Codex" });
    await user.clear(field);
    expect(screen.getByText("Use 1 to 64 characters.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.type(field, "  Ruthe reviewer {Enter}");
    expect(await screen.findByRole("listitem", { name: "Ruthe reviewer" })).toBeInTheDocument();
    const patch = calls.filter((c) => c.method === "PATCH");
    expect(patch.map((c) => [c.path, c.body])).toEqual([
      ["/agents/r2", { display_name: "Ruthe reviewer" }],
    ]);
  });

  it("cancels a rename with Escape", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: fleet() }),
    });
    const user = userEvent.setup();
    renderRoute("/agents");
    await user.click(await screen.findByRole("button", { name: "Rename builder" }));
    await user.type(screen.getByRole("textbox", { name: "New name for builder" }), "x{Escape}");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(row("builder")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("keeps a temporary agent (promotes it to fixed)", async () => {
    let agents = fleet();
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": () => json(200, { items: agents }),
      "PATCH /agents/r1": () => {
        agents = agents.map((a) => (a.id === "r1" ? { ...a, kind: "fixed" as const } : a));
        return json(
          200,
          agents.find((a) => a.id === "r1"),
        );
      },
    });
    const user = userEvent.setup();
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Ruthe" });
    expect(within(row("builder")).queryByRole("button", { name: /^Keep/ })).toBeNull();
    await user.click(
      within(row("Ruthe · Claude Code")).getByRole("button", { name: "Keep Ruthe · Claude Code" }),
    );
    await waitFor(() =>
      expect(within(row("Ruthe · Claude Code")).queryByText("Temporary")).toBeNull(),
    );
    expect(calls.filter((c) => c.method === "PATCH").map((c) => [c.path, c.body])).toEqual([
      ["/agents/r1", { fixed: true }],
    ]);
  });

  it("renames a project the user owns, not one shared with them", async () => {
    let agents = [
      ...fleet(),
      makeAgent({
        id: "s1",
        name: "helper",
        my_level: "use",
        owner: { id: "u2", username: "bruno", display_name: "Bruno" },
        project: { id: "p9", slug: "theirs", name: "Theirs" },
      }),
    ];
    const { calls } = mockApi({
      "GET /me": json(200, me),
      "GET /agents": () => json(200, { items: agents }),
      "PATCH /projects/p1": () => {
        agents = agents.map((a) =>
          a.project?.id === "p1" ? { ...a, project: { ...ruthe, name: "Videos" } } : a,
        );
        return json(200, { ...ruthe, name: "Videos" });
      },
    });
    const user = userEvent.setup();
    renderRoute("/agents");
    await screen.findByRole("list", { name: "Agents of Theirs" });
    expect(screen.queryByRole("button", { name: "Rename project Theirs" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Rename project No project" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Rename project Ruthe" }));
    const field = screen.getByRole("textbox", { name: "Rename project Ruthe" });
    await user.clear(field);
    await user.type(field, "Videos{Enter}");
    expect(await screen.findByRole("heading", { name: "Videos" })).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PATCH").map((c) => [c.path, c.body])).toEqual([
      ["/projects/p1", { name: "Videos" }],
    ]);
  });
});
