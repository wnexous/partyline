import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Level, Room, RoomMember } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import { makeAgent, makeChat, makeMe, makeMember, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

const members: RoomMember[] = [
  makeMember({ id: "mb-bot", member_id: "a1", name: "release-bot", online: true }),
  makeMember({
    id: "mb-ops",
    kind: "chat",
    member_id: "c1",
    name: "Ops on call",
    online: undefined,
    owner: undefined,
    connector_kind: "telegram",
    chat_kind: "group",
  }),
];

function roomApi(room: Partial<Room> = {}, extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", ...room })),
    "GET /rooms/r1/members": json(200, { items: members }),
    "GET /rooms/r1/messages": json(200, { items: [], has_more: false }),
    "GET /agents": json(200, {
      items: [
        makeAgent({ id: "a1", name: "release-bot", online: true }),
        makeAgent({ id: "a2", name: "triage", online: false }),
        makeAgent({ id: "a3", name: "docs-writer", my_level: "use", online: true }),
        // Not usable: never offered.
        makeAgent({ id: "a9", name: "someone-elses", my_level: "view" as Level }),
      ],
    }),
    "GET /chats": json(200, {
      items: [
        makeChat({ id: "c1", title: "Ops on call" }),
        makeChat({ id: "c3", title: "Release notes", kind: "channel" }),
        makeChat({ id: "c4", title: "Support team", connector_kind: "whatsapp" }),
      ],
    }),
    ...extra,
  });
}

const membersPanel = () => screen.getByRole("complementary", { name: "Members" });

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("members panel (UI-10, UI-12)", () => {
  it("lists members with the agents' online state", async () => {
    roomApi();
    renderRoute("/rooms/r1");
    const panel = await screen.findByRole("complementary", { name: "Members" });
    const bot = await within(panel).findByTestId("member-mb-bot");
    expect(within(bot).getByText("release-bot")).toBeInTheDocument();
    expect(within(bot).getByText("Online")).toBeInTheDocument();
    expect(within(panel).getByTestId("member-mb-ops")).toHaveTextContent("Ops on call");
  });

  it("follows live presence for agent members", async () => {
    roomApi();
    const { queryClient } = renderRoute("/rooms/r1");
    const bot = await screen.findByTestId("member-mb-bot");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: false },
      }),
    );
    await waitFor(() => expect(within(bot).getByText("Offline")).toBeInTheDocument());
  });

  it("toggles a chat member's outbound flag with PATCH", async () => {
    const { calls } = roomApi(
      {},
      {
        "PATCH /rooms/r1/members/mb-ops": json(200, { ...members[1], outbound: false }),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const outbound = await screen.findByRole("switch", { name: /Outbound to Ops on call/ });
    expect(outbound).toBeChecked();
    await user.click(outbound);
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")).toMatchObject({
        path: "/rooms/r1/members/mb-ops",
        body: { outbound: false },
      }),
    );
    await waitFor(() => expect(outbound).not.toBeChecked());
  });

  it("removes a member with DELETE", async () => {
    const { calls } = roomApi(
      {},
      { "DELETE /rooms/r1/members/mb-bot": new Response(null, { status: 204 }) },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Remove release-bot" }));
    await waitFor(() =>
      expect(
        calls.some((c) => c.method === "DELETE" && c.path === "/rooms/r1/members/mb-bot"),
      ).toBe(true),
    );
  });

  it("is read-only for people without manage", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByTestId("member-mb-ops");
    expect(within(membersPanel()).queryByRole("switch")).toBeNull();
    expect(within(membersPanel()).getByText("Outbound on")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Remove/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "Add members" })).toBeNull();
  });
});

describe("member picker (UI-10)", () => {
  it("offers only usable agents and chats that are not members yet, filterable by text", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });

    const triage = await within(dialog).findByTestId("candidate-agent-a2");
    expect(triage).toHaveTextContent("triage");
    expect(triage).toHaveTextContent("Offline");
    expect(within(dialog).getByTestId("candidate-agent-a3")).toHaveTextContent("Online");
    expect(within(dialog).getByTestId("candidate-chat-c3")).toHaveTextContent("Release notes");
    expect(within(dialog).getByTestId("candidate-chat-c4")).toHaveTextContent("Support team");
    // Already members, or not usable.
    expect(within(dialog).queryByTestId("candidate-agent-a1")).toBeNull();
    expect(within(dialog).queryByTestId("candidate-chat-c1")).toBeNull();
    expect(within(dialog).queryByTestId("candidate-agent-a9")).toBeNull();

    await user.type(within(dialog).getByRole("searchbox", { name: "Filter" }), "SUPP");
    expect(within(dialog).getByTestId("candidate-chat-c4")).toBeInTheDocument();
    expect(within(dialog).queryByTestId("candidate-chat-c3")).toBeNull();
    expect(within(dialog).queryByTestId("candidate-agent-a2")).toBeNull();
  });

  it("adds the picked chat as a member", async () => {
    const { calls } = roomApi(
      {},
      {
        "POST /rooms/r1/members": json(
          201,
          makeMember({ id: "mb-new", kind: "chat", member_id: "c3", name: "Release notes" }),
        ),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    const row = await within(dialog).findByTestId("candidate-chat-c3");
    await user.click(within(row).getByRole("button", { name: "Add Release notes" }));
    await waitFor(() =>
      expect(
        calls.find((c) => c.method === "POST" && c.path === "/rooms/r1/members")?.body,
      ).toEqual({ kind: "chat", member_id: "c3" }),
    );
  });
});

describe("loop guard banner (UI-08)", () => {
  it("shows a resume button to managers that posts /resume", async () => {
    const { calls } = roomApi(
      { agents_paused: true },
      { "POST /rooms/r1/resume": json(200, makeRoom({ id: "r1", agents_paused: false })) },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    expect(await screen.findByText(/Agents are paused/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Resume agents" }));
    await waitFor(() =>
      expect(calls.some((c) => c.method === "POST" && c.path === "/rooms/r1/resume")).toBe(true),
    );
    await waitFor(() => expect(screen.queryByText(/Agents are paused/)).toBeNull());
  });

  it("shows the banner without the button to non-managers", async () => {
    roomApi({ agents_paused: true, my_level: "post" });
    renderRoute("/rooms/r1");
    expect(await screen.findByText(/Agents are paused/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Resume agents" })).toBeNull();
  });

  it("is hidden while agents run", async () => {
    roomApi({ agents_paused: false });
    renderRoute("/rooms/r1");
    await screen.findByTestId("member-mb-bot");
    expect(screen.queryByText(/Agents are paused/)).toBeNull();
  });
});

describe("room settings", () => {
  it("saves name, bridge chats and retention with PATCH", async () => {
    const { calls } = roomApi(
      {},
      { "PATCH /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys-prod" })) },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    const name = within(sheet).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "deploys-prod");
    await user.click(within(sheet).getByRole("switch", { name: /Bridge chats/ }));
    const retention = within(sheet).getByLabelText(/Keep messages for/);
    await user.clear(retention);
    await user.type(retention, "0");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH" && c.path === "/rooms/r1")?.body).toEqual({
        name: "deploys-prod",
        description: "",
        bridge_chats: true,
        retention_days: 0,
      }),
    );
  });

  it("is not offered to people without manage", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByTestId("member-mb-bot");
    expect(screen.queryByRole("button", { name: "Room settings" })).toBeNull();
  });
});
