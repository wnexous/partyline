import { act, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeContacts, makeMe } from "@/test/fixtures";
import { handleLiveEvent, useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The "Connect an agent" wizard (WIZ-01), opened from the contacts page: the install and setup
 * commands of this instance, each harness's start command and how messages reach it, and the
 * manual entries. It moved here from the agents page, which became the contacts page.
 */

const me = makeMe();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {} }));
afterEach(() => vi.unstubAllGlobals());

/** Text of a labelled code block (CodeBlock renders a figure captioned with its label). */
const code = (scope: HTMLElement, name: string) =>
  within(scope).getByRole("figure", { name }).querySelector("pre")?.textContent ?? "";

describe("connect an agent (WIZ-01)", () => {
  const harnesses = [
    { tab: "Claude Code", start: "partyline claude" },
    { tab: "OpenCode", start: "partyline opencode" },
    { tab: "pi", start: "partyline pi" },
    { tab: "omp", start: "partyline omp" },
  ];
  const PUSH = "Room messages arrive in the running session as they come in.";

  async function openWizard(routes: Parameters<typeof mockApi>[0] = {}) {
    const api = mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(200, makeContacts()),
      "GET /agents": json(200, { items: [] }),
      ...routes,
    });
    const user = userEvent.setup();
    const utils = renderRoute("/contacts");
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
      expect(within(panel).getByText(PUSH)).toBeInTheDocument();
    }
  });

  it("offers only the four harnesses with a live channel (AD-035)", async () => {
    const { dialog } = await openWizard();
    const tabs = within(dialog).getByRole("tablist", { name: "Harness" });
    const names = within(tabs)
      .getAllByRole("tab")
      .map((tab) => tab.textContent);
    expect(names).toEqual(["Claude Code", "OpenCode", "pi", "omp"]);
    for (const gone of ["Codex", "Gemini", "Antigravity"]) {
      expect(within(tabs).queryByRole("tab", { name: gone })).toBeNull();
    }
    expect(dialog.textContent).not.toMatch(
      /wait_for_messages|partyline codex|partyline gemini|partyline agy/,
    );
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
    // No credential is ever inlined in a manual entry.
    for (const text of [JSON.stringify(claude), code(dialog, "~/.omp/agent/mcp.json")]) {
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
