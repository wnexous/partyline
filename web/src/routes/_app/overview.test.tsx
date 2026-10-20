import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeConnector,
  makeConnectorKind,
  makeMe,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const me = makeMe();

beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, connectors: {} }));
afterEach(() => vi.unstubAllGlobals());

const section = (name: string) => screen.getByRole("region", { name });

function busyInstance() {
  return mockApi({
    "GET /me": json(200, me),
    "GET /agents": json(200, {
      items: [
        makeAgent({ id: "a1", name: "release-bot", online: true }),
        makeAgent({ id: "a2", name: "reviewer", online: false }),
      ],
    }),
    "GET /connectors": json(200, {
      items: [
        makeConnector({ id: "k1", name: "ops-bot", status: "connected" }),
        makeConnector({
          id: "k2",
          kind: "discord",
          name: "guild-bridge",
          status: "error",
          last_error: "invalid bot token",
        }),
      ],
    }),
    // The rooms list only decides between the "no rooms" and the feed states; row room names
    // come from the feed itself (ActivityItem.room).
    "GET /rooms": json(200, { items: [makeRoom({ id: "r1", name: "general", last_seq: 2 })] }),
    "GET /activity": json(200, {
      items: [
        {
          room: { id: "r1", name: "general" },
          message: makeMessage({ room_id: "r1", seq: 2, text: "tests are green" }),
        },
        {
          room: { id: "r1", name: "general" },
          message: makeMessage({
            room_id: "r1",
            seq: 1,
            text: "running tests",
            created_at: "2026-10-19T11:59:00Z",
          }),
        },
        {
          room: { id: "r2", name: "deploys" },
          message: makeMessage({
            room_id: "r2",
            seq: 1,
            author_kind: "agent",
            author_name: "release-bot",
            text: "v1.2 shipped",
            created_at: "2026-10-19T11:00:00Z",
          }),
        },
      ],
    }),
  });
}

describe("dashboard (UI-15, UI-17)", () => {
  it("shows online agents, connector health, recent activity and quick actions", async () => {
    const { calls } = busyInstance();
    renderRoute("/");

    const agents = await screen.findByRole("region", { name: "Agents" });
    expect(await within(agents).findByText("1 of 2 online")).toBeInTheDocument();
    const agentRows = within(agents).getAllByRole("listitem");
    expect(agentRows.map((r) => r.getAttribute("aria-label"))).toEqual(["release-bot", "reviewer"]);
    expect(within(agentRows[0] as HTMLElement).getByText("Online")).toBeInTheDocument();
    expect(within(agentRows[1] as HTMLElement).getByText("Offline")).toBeInTheDocument();

    const connectors = section("Connectors");
    expect(await within(connectors).findByText("1 of 2 connected")).toBeInTheDocument();
    // Connectors that need attention come first, with their last error.
    const connectorRows = within(connectors).getAllByRole("listitem");
    expect(connectorRows.map((r) => r.getAttribute("aria-label"))).toEqual([
      "guild-bridge",
      "ops-bot",
    ]);
    expect(within(connectorRows[0] as HTMLElement).getByText("Error")).toBeInTheDocument();
    expect(
      within(connectorRows[0] as HTMLElement).getByText("invalid bot token"),
    ).toBeInTheDocument();
    expect(within(connectorRows[1] as HTMLElement).getByText("Connected")).toBeInTheDocument();

    // Recent activity: newest first across rooms, each row opens its room.
    const activity = section("Recent activity");
    const feed = await within(activity).findByRole("list", { name: "Latest messages" });
    const items = within(feed).getAllByRole("listitem");
    expect(items.map((i) => i.textContent)).toEqual([
      expect.stringContaining("tests are green"),
      expect.stringContaining("running tests"),
      expect.stringContaining("v1.2 shipped"),
    ]);
    expect(within(items[2] as HTMLElement).getByText("deploys")).toBeInTheDocument();
    expect(within(items[0] as HTMLElement).getByRole("link")).toHaveAttribute("href", "/rooms/r1");
    expect(within(items[2] as HTMLElement).getByRole("link")).toHaveAttribute("href", "/rooms/r2");
    // One feed request, no per-room sampling.
    const feedCalls = calls.filter((c) => c.path === "/activity");
    expect(feedCalls).toHaveLength(1);
    expect(Number(feedCalls[0]?.url.searchParams.get("limit"))).toBeGreaterThan(0);
    expect(calls.filter((c) => /^\/rooms\/[^/]+\/messages$/.test(c.path))).toEqual([]);

    const actions = screen.getByRole("group", { name: "Quick actions" });
    for (const name of ["Connect an agent", "Add connector", "New room"]) {
      expect(within(actions).getByRole("button", { name })).toBeInTheDocument();
    }
  });

  it("updates live: presence, connector status and new messages", async () => {
    busyInstance();
    const { queryClient } = renderRoute("/");
    const agents = await screen.findByRole("region", { name: "Agents" });
    await within(agents).findByText("1 of 2 online");
    const feed = await screen.findByRole("list", { name: "Latest messages" });
    await within(section("Connectors")).findByText("1 of 2 connected");

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a2", online: true, last_seen_at: "2026-10-19T12:05:00Z" },
      }),
    );
    expect(within(agents).getByText("2 of 2 online")).toBeInTheDocument();
    const reviewer = within(agents).getByRole("listitem", { name: "reviewer" });
    expect(within(reviewer).getByText("Online")).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "connector.status",
        data: { connector_id: "k1", status: "error", last_error: "network unreachable" },
      }),
    );
    const connectors = section("Connectors");
    expect(within(connectors).getByText("0 of 2 connected")).toBeInTheDocument();
    const opsBot = within(connectors).getByRole("listitem", { name: "ops-bot" });
    expect(within(opsBot).getByText("Error")).toBeInTheDocument();
    expect(within(opsBot).getByText("network unreachable")).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.message",
        data: makeMessage({
          room_id: "r2",
          seq: 2,
          text: "rollback finished",
          created_at: "2026-10-19T12:10:00Z",
        }),
      }),
    );
    // The query cache notifies observers on the next tick.
    await waitFor(() =>
      expect(within(feed).getAllByRole("listitem")[0]).toHaveTextContent("rollback finished"),
    );
    const first = within(feed).getAllByRole("listitem")[0] as HTMLElement;
    expect(within(first).getByText("deploys")).toBeInTheDocument();
  });

  it("empty states open the connect, add connector and new room flows", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [] }),
      "GET /rooms": json(200, { items: [] }),
      "GET /connector-kinds": json(200, { items: [makeConnectorKind()] }),
    });
    const user = userEvent.setup();
    renderRoute("/");

    const agents = await screen.findByRole("region", { name: "Agents" });
    await within(agents).findByText("No agents yet");
    await user.click(within(agents).getByRole("button", { name: "Connect an agent" }));
    expect(await screen.findByRole("dialog", { name: "Connect an agent" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    const connectors = section("Connectors");
    await within(connectors).findByText("No connectors yet");
    await user.click(within(connectors).getByRole("button", { name: "Add connector" }));
    expect(await screen.findByRole("dialog", { name: "Add connector" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    const activity = section("Recent activity");
    await within(activity).findByText("No rooms yet");
    await user.click(within(activity).getByRole("button", { name: "New room" }));
    expect(await screen.findByRole("dialog", { name: "New room" })).toBeInTheDocument();
  });

  it("a quiet line points to the rooms page", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [makeAgent()] }),
      "GET /connectors": json(200, { items: [makeConnector()] }),
      "GET /rooms": json(200, { items: [makeRoom({ last_seq: 0 })] }),
      "GET /activity": json(200, { items: [] }),
    });
    renderRoute("/");
    const activity = await screen.findByRole("region", { name: "Recent activity" });
    await within(activity).findByText("The line is quiet");
    expect(within(activity).getByRole("link", { name: "Open rooms" })).toHaveAttribute(
      "href",
      "/rooms",
    );
  });
});

describe("dashboard activity errors (UI-17)", () => {
  it("shows the panel error when the activity feed fails, and retries it", async () => {
    let activityCalls = 0;
    mockApi({
      "GET /me": json(200, me),
      "GET /agents": json(200, { items: [] }),
      "GET /connectors": json(200, { items: [] }),
      "GET /rooms": json(200, { items: [makeRoom({ id: "r1", name: "general", last_seq: 2 })] }),
      "GET /activity": () => {
        activityCalls++;
        return json(500, { error: { code: "internal", message: "boom" } });
      },
    });
    const user = userEvent.setup();
    renderRoute("/");
    const activity = await screen.findByRole("region", { name: "Recent activity" });
    const alert = await within(activity).findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load this section.");
    expect(within(activity).queryByText(/quiet|no activity/i)).toBeNull();
    const before = activityCalls;
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(activityCalls).toBeGreaterThan(before));
  });
});
