import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "@/lib/api/queries";
import type { Level, Room, RoomMember } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAgent, makeChat, makeMe, makeMember, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { makeQueryClient, renderRoute } from "@/test/render";

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

describe("agent membership settings (MEM-01..05)", () => {
  it("offers settings for agent members only, to managers, and shows the saved settings", async () => {
    const { calls } = roomApi(
      {},
      {
        "PATCH /rooms/r1/members/mb-bot": json(200, {
          ...members[0],
          display_name: "Bia",
          sign_outbound: false,
          delivery: "mentions",
        }),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const bot = await screen.findByTestId("member-mb-bot");
    expect(screen.queryByRole("button", { name: "Settings for Ops on call" })).toBeNull();

    await user.click(within(bot).getByRole("button", { name: "Settings for release-bot" }));
    const dialog = await screen.findByRole("dialog", { name: "Agent settings" });
    await user.type(within(dialog).getByRole("textbox", { name: /Display name/ }), "Bia");
    await user.click(within(dialog).getByRole("switch", { name: "Sign messages" }));
    await user.click(within(dialog).getByRole("radio", { name: /Only mentions/ }));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() =>
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({
        display_name: "Bia",
        sign_outbound: false,
        delivery: "mentions",
      }),
    );
    await waitFor(() => expect(bot).toHaveTextContent("as Bia"));
    expect(bot).toHaveTextContent("unsigned");
    expect(bot).toHaveTextContent("mentions only");
  });

  it("hides the settings from people without manage", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByTestId("member-mb-bot");
    expect(screen.queryByRole("button", { name: /^Settings for/ })).toBeNull();
  });
});

describe("chat titles (CTX-07)", () => {
  const untitled = makeMember({
    id: "mb-dm",
    kind: "chat",
    member_id: "c5",
    name: "",
    online: undefined,
    owner: undefined,
    connector_kind: "whatsapp",
    chat_kind: "dm",
  });

  it("names an untitled chat member after its platform", async () => {
    roomApi({}, { "GET /rooms/r1/members": json(200, { items: [...members, untitled] }) });
    renderRoute("/rooms/r1");
    const row = await screen.findByTestId("member-mb-dm");
    expect(within(row).getByText("WhatsApp chat")).toBeInTheDocument();
    expect(within(row).getByRole("switch", { name: /Outbound to WhatsApp chat/ })).toBeChecked();
    expect(within(row).getByRole("button", { name: "Remove WhatsApp chat" })).toBeInTheDocument();
    // Titled chats keep their title.
    expect(screen.getByTestId("member-mb-ops")).toHaveTextContent("Ops on call");
  });

  it("names an untitled chat in the add-member picker and finds it by that name", async () => {
    const { calls } = roomApi(
      {},
      {
        "GET /chats": json(200, {
          items: [makeChat({ id: "c6", title: "", kind: "dm", connector_kind: "telegram" })],
        }),
        "POST /rooms/r1/members": json(201, makeMember({ kind: "chat", member_id: "c6" })),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    const candidate = await within(dialog).findByTestId("candidate-chat-c6");
    expect(within(candidate).getByText("Telegram chat")).toBeInTheDocument();
    await user.type(within(dialog).getByRole("searchbox", { name: "Filter" }), "telegram chat");
    await user.click(within(dialog).getByRole("button", { name: "Add Telegram chat" }));
    await waitFor(() =>
      expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({
        kind: "chat",
        member_id: "c6",
      }),
    );
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

describe("projects in rooms (PROJ-06, PROJ-07)", () => {
  const ruthe = { id: "p1", slug: "ruthe", name: "Ruthe" };
  const projects = {
    items: [
      {
        ...ruthe,
        owner: { id: "u1", username: "ana", display_name: "Ana Souza" },
        my_level: "manage",
        created_at: "2026-10-01T12:00:00Z",
      },
      {
        id: "p2",
        slug: "web",
        name: "Web",
        owner: { id: "u2", username: "bruno", display_name: "Bruno" },
        my_level: "use",
        created_at: "2026-10-01T12:00:00Z",
      },
    ],
  };
  const agents = {
    items: [
      makeAgent({ id: "a1", name: "release-bot", online: true }),
      makeAgent({
        id: "t1",
        name: "ruthe-claude",
        display_name: "Ruthe · Claude Code",
        kind: "temporary",
        project: ruthe,
        online: true,
      }),
      makeAgent({ id: "f1", name: "zeta-bot", display_name: "zeta-bot" }),
      makeAgent({ id: "a2", name: "triage", online: false }),
    ],
  };

  it("offers projects first as any-session members, then fixed agents before temporary ones", async () => {
    const { calls } = roomApi(
      {},
      {
        "GET /projects": json(200, projects),
        "GET /agents": json(200, agents),
        "POST /rooms/r1/members": json(
          201,
          makeMember({ id: "mb-p", kind: "project", member_id: "p1", name: "Ruthe" }),
        ),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });

    const sections = await within(dialog).findAllByRole("region");
    expect(sections.map((r) => r.getAttribute("aria-label"))).toEqual([
      "Projects",
      "Agents",
      "Chats",
    ]);
    const project = within(dialog).getByTestId("candidate-project-p1");
    expect(project).toHaveTextContent("Ruthe (any session)");
    expect(project).toHaveTextContent("1 online");
    expect(within(dialog).getByTestId("candidate-project-p2")).toHaveTextContent(
      "No session online",
    );
    const agentIds = within(within(dialog).getByRole("region", { name: "Agents" }))
      .getAllByRole("listitem")
      .map((li) => li.getAttribute("data-testid"));
    expect(agentIds).toEqual(["candidate-agent-a2", "candidate-agent-f1", "candidate-agent-t1"]);
    expect(within(dialog).getByTestId("candidate-agent-t1")).toHaveTextContent(
      "Ruthe · Claude Code",
    );
    expect(within(dialog).getByTestId("candidate-agent-t1")).toHaveTextContent("Temporary");

    await user.click(within(project).getByRole("button", { name: "Add Ruthe (any session)" }));
    await waitFor(() =>
      expect(
        calls.find((c) => c.method === "POST" && c.path === "/rooms/r1/members")?.body,
      ).toEqual({ kind: "project", member_id: "p1" }),
    );
  });

  it("does not offer a project that is already a member", async () => {
    roomApi(
      {},
      {
        "GET /projects": json(200, projects),
        "GET /rooms/r1/members": json(200, {
          items: [...members, makeMember({ id: "mb-p", kind: "project", member_id: "p1" })],
        }),
      },
    );
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Add members" }));
    const dialog = await screen.findByRole("dialog", { name: "Add members" });
    expect(await within(dialog).findByTestId("candidate-project-p2")).toBeInTheDocument();
    expect(within(dialog).queryByTestId("candidate-project-p1")).toBeNull();
  });

  it("shows a project member's active session and refreshes it on members.changed", async () => {
    let projectMember = makeMember({
      id: "mb-p",
      kind: "project",
      member_id: "p1",
      name: "Ruthe",
      online: true,
      active_session: { id: "t1", name: "ruthe-claude", display_name: "Ruthe · Claude Code" },
    });
    roomApi(
      {},
      {
        "GET /rooms/r1/members": () => json(200, { items: [...members, projectMember] }),
      },
    );
    const { queryClient } = renderRoute("/rooms/r1");
    const panel = await screen.findByRole("complementary", { name: "Members" });
    const group = await within(panel).findByRole("region", { name: "Projects" });
    const row = within(group).getByTestId("member-mb-p");
    expect(row).toHaveTextContent("Ruthe");
    expect(row).toHaveTextContent("Active: Ruthe · Claude Code");

    projectMember = { ...projectMember, online: false, active_session: undefined };
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "room.members",
        data: { room_id: "r1" },
      }),
    );
    await waitFor(() =>
      expect(within(panel).getByTestId("member-mb-p")).toHaveTextContent(
        "No session online; messages wait for the next one",
      ),
    );
  });

  it("refreshes the projects a user can add when a project's access changes", () => {
    const qc = makeQueryClient();
    qc.setQueryData(queryKeys.projects, projects.items);
    handleLiveEvent(qc, useLiveStore.getState(), {
      type: "access.changed",
      data: { resource_kind: "project", resource_id: "p2" },
    });
    expect(qc.getQueryState(queryKeys.projects)?.isInvalidated).toBe(true);
  });
});

describe("composer by access level (UI-07)", () => {
  it("shows a read-only notice and no composer to view-only users", async () => {
    roomApi({ my_level: "view" });
    renderRoute("/rooms/r1");
    expect(await screen.findByText(/You can read this room but not post/)).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Message deploys" })).toBeNull();
  });

  it("shows the composer to users with post", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    expect(await screen.findByRole("textbox", { name: "Message deploys" })).toBeInTheDocument();
    expect(screen.queryByText(/You can read this room but not post/)).toBeNull();
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
    expect(screen.queryByRole("button", { name: "Share" })).toBeNull();
  });
});

describe("room instructions (INS-01)", () => {
  async function openSettings(room: Partial<Room>, patch: Response) {
    const api = roomApi(room, { "PATCH /rooms/r1": patch });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    const box = within(sheet).getByRole("textbox", { name: /Instructions for agents/ });
    const patches = () => api.calls.filter((c) => c.method === "PATCH");
    return { user, sheet, box, patches };
  }

  it("shows the current instructions and saves the edited text", async () => {
    const { user, sheet, box, patches } = await openSettings(
      { instructions: "Answer in Portuguese." },
      json(200, makeRoom({ id: "r1", instructions: "Answer in Portuguese. Be brief." })),
    );
    expect(box).toHaveValue("Answer in Portuguese.");
    expect(within(sheet).getByText("21 / 4000")).toBeInTheDocument();
    // A11Y-03: the counter describes the textarea.
    expect(box).toHaveAccessibleDescription(/21 \/ 4000/);
    await user.type(box, " Be brief.");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({
      name: "deploys",
      description: "",
      bridge_chats: false,
      retention_days: 90,
      instructions: "Answer in Portuguese. Be brief.",
    });
  });

  it("allows 4000 characters counted as the server counts them", async () => {
    const { user, sheet, box, patches } = await openSettings({}, json(200, makeRoom({ id: "r1" })));
    // 4000 emoji are 8000 UTF-16 units but 4000 characters.
    await user.click(box);
    await user.paste("🙂".repeat(4000));
    expect(within(sheet).getByText("4000 / 4000")).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toMatchObject({ instructions: "🙂".repeat(4000) });
  });

  it("blocks 4001 characters before sending", async () => {
    const { user, sheet, box, patches } = await openSettings({}, json(200, makeRoom({ id: "r1" })));
    await user.click(box);
    await user.paste("a".repeat(4001));
    expect(within(sheet).getByText("4001 / 4000")).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("At most 4000 characters")).toBeInTheDocument();
    expect(box).toHaveAttribute("aria-invalid", "true");
    expect(patches()).toHaveLength(0);
  });

  it("shows a 422 instructions reason under the field", async () => {
    const { user, sheet, box } = await openSettings(
      {},
      apiError(422, "validation", "invalid", {
        instructions: "must have at most 4000 characters",
      }),
    );
    await user.type(box, "Be brief.");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(await within(sheet).findByText("must have at most 4000 characters")).toBeInTheDocument();
    expect(box).toHaveAttribute("aria-invalid", "true");
  });
});

describe("delete room (ROOMDEL-01, ROOMDEL-02)", () => {
  async function openDelete(extra: Parameters<typeof mockApi>[0]) {
    const api = roomApi({}, extra);
    const queryClient = makeQueryClient();
    queryClient.setQueryData(queryKeys.rooms, [
      makeRoom({ id: "r1", name: "deploys" }),
      makeRoom({ id: "r2", name: "support" }),
    ]);
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1", queryClient);
    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    await user.click(within(sheet).getByRole("button", { name: "Delete room" }));
    const alert = await screen.findByRole("alertdialog", { name: "Delete deploys?" });
    const deletes = () => api.calls.filter((c) => c.method === "DELETE" && c.path === "/rooms/r1");
    return { user, router, queryClient, alert, deletes };
  }

  it("confirms, deletes, returns to /rooms and drops the room from the list", async () => {
    const { user, router, queryClient, alert, deletes } = await openDelete({
      "DELETE /rooms/r1": new Response(null, { status: 204 }),
      // The list refetch never answers: only the cache update can drop the room.
      "GET /rooms": () => new Promise<Response>(() => {}),
    });
    expect(alert).toHaveTextContent(
      "Its messages, attachments and pending deliveries are removed. This can’t be undone.",
    );
    expect(deletes()).toHaveLength(0);
    await user.click(within(alert).getByRole("button", { name: "Delete room" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms"));
    expect(deletes()).toHaveLength(1);
    expect(await screen.findByText("deploys deleted")).toBeInTheDocument();
    expect(queryClient.getQueryData<Room[]>(queryKeys.rooms)?.map((r) => r.id)).toEqual(["r2"]);
    expect(await screen.findByRole("link", { name: /support/ })).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /deploys/ })).toBeNull();
  });

  it("keeps the room when the confirmation is cancelled", async () => {
    const { user, router, alert, deletes } = await openDelete({
      "DELETE /rooms/r1": new Response(null, { status: 204 }),
    });
    await user.click(within(alert).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(deletes()).toHaveLength(0);
    expect(router.state.location.pathname).toBe("/rooms/r1");
  });

  it("stays on the room and says so when the deletion fails", async () => {
    const { user, router, queryClient, alert, deletes } = await openDelete({
      "DELETE /rooms/r1": apiError(500, "internal"),
    });
    await user.click(within(alert).getByRole("button", { name: "Delete room" }));
    expect(await screen.findByText("Couldn’t delete the room.")).toBeInTheDocument();
    expect(deletes()).toHaveLength(1);
    expect(router.state.location.pathname).toBe("/rooms/r1");
    expect(queryClient.getQueryData<Room[]>(queryKeys.rooms)?.map((r) => r.id)).toEqual([
      "r1",
      "r2",
    ]);
  });

  it("is not offered to people without manage", async () => {
    roomApi({ my_level: "post" });
    renderRoute("/rooms/r1");
    await screen.findByTestId("member-mb-bot");
    expect(screen.queryByRole("button", { name: "Delete room" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Room settings" })).toBeNull();
  });
});

describe("room sharing entry (UI-11)", () => {
  it("opens the sharing dialog for this room", async () => {
    roomApi({}, { "GET /rooms/r1/sharing": json(200, { mode: "private", users: [] }) });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Share" }));
    const dialog = await screen.findByRole("dialog", { name: "Share deploys" });
    expect(await within(dialog).findByRole("radio", { name: /Private/ })).toBeChecked();
  });
});

describe("broadcast setting (ROUTE-06, ROUTE-07)", () => {
  async function openSettings(
    room: Partial<Room>,
    patch: Response = json(200, makeRoom({ id: "r1" })),
  ) {
    const api = roomApi(room, { "PATCH /rooms/r1": patch });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    const patches = () => api.calls.filter((c) => c.method === "PATCH");
    return { user, sheet, patches };
  }

  it("shows broadcast on and sends it only when turned off", async () => {
    const { user, sheet, patches } = await openSettings({ broadcast: true });
    const toggle = within(sheet).getByRole("switch", { name: /Send to every chat/ });
    expect(toggle).toBeChecked();
    await user.click(toggle);
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({
      name: "deploys",
      description: "",
      bridge_chats: false,
      retention_days: 90,
      broadcast: false,
    });
  });

  it("locks broadcast and bridging off in a catch-all room and says why", async () => {
    const { sheet } = await openSettings({
      broadcast: false,
      bridge_chats: false,
      catch_all: true,
    });
    const toggle = within(sheet).getByRole("switch", { name: /Send to every chat/ });
    expect(toggle).not.toBeChecked();
    expect(toggle).toBeDisabled();
    const bridge = within(sheet).getByRole("switch", { name: /Bridge chats/ });
    expect(bridge).toBeDisabled();
    const locked =
      "This is a catch-all room: posts only reach the chats you pick, and chats are not bridged.";
    expect(toggle).toHaveAccessibleDescription(locked);
    expect(bridge).toHaveAccessibleDescription(locked);
  });

  it("shows a 422 broadcast reason", async () => {
    const { user, sheet } = await openSettings(
      { broadcast: false },
      apiError(422, "validation", "invalid", {
        broadcast: "must stay off while the room is a catch-all room",
      }),
    );
    await user.click(within(sheet).getByRole("switch", { name: /Send to every chat/ }));
    await user.click(within(sheet).getByRole("button", { name: "Save" }));
    expect(
      await within(sheet).findByText("must stay off while the room is a catch-all room"),
    ).toBeInTheDocument();
  });

  it("asks the composer for an explicit chat when the room does not broadcast", async () => {
    roomApi({ broadcast: false });
    renderRoute("/rooms/r1");
    expect(await screen.findByText("Answer a chat explicitly")).toBeInTheDocument();
  });
});
