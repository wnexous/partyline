import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { IntelligenceProfile, SessionInfo } from "@/shared/api/types.gen";
import { json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeIntelligence,
  makeMe,
  makeMember,
  makePerson,
  makeProfile,
} from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders, renderWithRouter } from "@/test/render";
import { MemberPanel } from "./member-panel";

/*
 * The member panel of a group. Rooms hold intelligences only (CID-20, CID-22): the member list shows AIs and users and has no
 * chat row, so it also has no per-chat outbound control. A chat member left in old data stays
 * invisible instead of coming back as a switch. AIs and users are one list, in the order they
 * joined, with nothing that marks one kind (AD-024).
 */

const toast = vi.hoisted(() => Object.assign(vi.fn(), { error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

const atlas = makeAI({ id: "a1", display_name: "Atlas", handle: "atlas" });
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const members = [
  makeMember({ id: "mb-atlas", member_id: "a1", name: "Atlas", intelligence: atlas }),
  makeMember({
    id: "mb-bob",
    kind: "user",
    member_id: "u2",
    name: "Bob Lima",
    intelligence: bob,
    owner: undefined,
  }),
  makeMember({
    id: "mb-ops",
    kind: "chat",
    member_id: "c1",
    name: "Ops on call",
    connector_kind: "telegram",
    chat_kind: "group",
    online: undefined,
    owner: undefined,
  }),
];
const chatOnly = members[2] as (typeof members)[number];

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  toast.mockClear();
});
afterEach(() => vi.unstubAllGlobals());

describe("member panel, group (CID-20, CID-22)", () => {
  it("shows AIs and users and neither a chat row nor its outbound control", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(<MemberPanel roomId="r1" members={members} canManage loading={false} />);

    expect(await screen.findByTestId("member-mb-atlas")).toHaveTextContent("Atlas");
    expect(screen.getByTestId("member-mb-bob")).toHaveTextContent("Bob Lima");
    expect(screen.queryByTestId("member-mb-ops")).toBeNull();
    expect(screen.queryByText("Ops on call")).toBeNull();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByText(/Outbound/)).toBeNull();
  });

  it("lists AIs and users together in the order they joined, not split by kind (AD-024)", async () => {
    const joined = [
      makeMember({
        id: "mb-bob",
        kind: "user",
        member_id: "u2",
        name: "Bob Lima",
        intelligence: bob,
        owner: undefined,
      }),
      makeMember({ id: "mb-atlas", member_id: "a1", name: "Atlas", intelligence: atlas }),
      makeMember({
        id: "mb-cora",
        kind: "user",
        member_id: "u3",
        name: "Cora Dias",
        intelligence: makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" }),
        owner: undefined,
      }),
      makeMember({
        id: "mb-notes",
        member_id: "a2",
        name: "Notes",
        intelligence: makeAI({ id: "a2", display_name: "Notes", handle: "notes" }),
      }),
    ];
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <MemberPanel roomId="r1" members={joined} canManage={false} loading={false} />,
    );

    const list = await screen.findByRole("list", { name: "Members" });
    expect(
      within(list)
        .getAllByRole("listitem")
        .map((row) => row.getAttribute("data-testid")),
    ).toEqual(["member-mb-bob", "member-mb-atlas", "member-mb-cora", "member-mb-notes"]);
    // No "Agents" and "Users" sections, headings or counts: one list.
    expect(screen.queryAllByRole("region")).toEqual([]);
    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.queryByText(/^(Agents|Users)$/)).toBeNull();
  });

  it("marks a session muted here and opens its notifications for a manager (SN-08)", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /agents/a1/sessions/claude-2/notifications": json(200, {
        items: [{ room_id: "r1", name: "Support", kind: "group", notify: "mentions", unread: 2 }],
      }),
    });
    const online = makeMember({
      id: "mb-atlas",
      member_id: "a1",
      name: "Atlas",
      intelligence: atlas,
      online: true,
      sessions: [
        {
          label: "claude-2",
          harness: "claude",
          connected_at: "2026-10-28T14:05:00Z",
          notify: "mentions",
        },
      ],
    });
    renderWithRouter(<MemberPanel roomId="r1" members={[online]} canManage loading={false} />);

    const row = within(await screen.findByTestId("member-mb-atlas"));
    expect(row.getByTestId("session-muted")).toHaveTextContent(
      "Notifications off in this conversation",
    );
    await userEvent
      .setup()
      .click(row.getByRole("button", { name: "Notification settings of Atlas" }));
    expect(await screen.findByText("Notifications in this conversation")).toBeInTheDocument();
    expect(
      await screen.findByRole("switch", { name: "Notifications in Support" }),
    ).not.toBeChecked();
  });

  it("offers no notification control to someone who does not manage the AI (SN-08)", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    const online = makeMember({
      id: "mb-atlas",
      member_id: "a1",
      name: "Atlas",
      intelligence: atlas,
      online: true,
      sessions: [
        {
          label: "claude-2",
          harness: "claude",
          connected_at: "2026-10-28T14:05:00Z",
          notify: "mentions",
        },
      ],
    });
    renderWithRouter(
      <MemberPanel roomId="r1" members={[online]} canManage={false} loading={false} />,
    );
    const row = within(await screen.findByTestId("member-mb-atlas"));
    expect(row.getByTestId("session-muted")).toBeInTheDocument();
    expect(row.queryByRole("button", { name: /Notification settings/ })).toBeNull();
  });

  it("puts no kind badge next to a member, and gives an AI and a user the same row", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <MemberPanel roomId="r1" members={members} canManage={false} loading={false} />,
    );

    const ai = within(await screen.findByTestId("member-mb-atlas"));
    const user = within(screen.getByTestId("member-mb-bob"));
    for (const row of [ai, user]) {
      expect(row.getByTestId("intelligence-avatar")).toBeInTheDocument();
      expect(row.queryByTestId("kind-badge")).toBeNull();
      expect(row.queryByText(/^(AI|IA|Agent|Person|User)$/)).toBeNull();
    }
    // The @handle is what tells two names apart; the AI's presence is a fact only it has.
    expect(ai.getByText("@atlas")).toBeInTheDocument();
    expect(user.getByText("@bob")).toBeInTheDocument();
    expect(ai.getByText("Offline")).toBeInTheDocument();
    expect(user.queryByText(/line$/)).toBeNull();
  });

  it("renders the empty state when the room only still holds a chat", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <MemberPanel roomId="r1" members={[chatOnly]} canManage={false} loading={false} />,
    );

    expect(
      await screen.findByText("No members yet. Add members so messages have somewhere to go."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("member-mb-ops")).toBeNull();
  });

  it("undoes a removal by adding the member back with nothing but who it is", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, makeMe()),
      "DELETE /rooms/r1/members/mb-atlas": new Response(null, { status: 204 }),
      "POST /rooms/r1/members": json(201, members[0]),
    });
    renderWithRouter(<MemberPanel roomId="r1" members={members} canManage loading={false} />);

    await userEvent.setup().click(await screen.findByRole("button", { name: "Remove Atlas" }));
    await waitFor(() => expect(toast).toHaveBeenCalledTimes(1));
    const options = toast.mock.calls[0]?.[1] as { action: { label: string; onClick: () => void } };
    expect(options.action.label).toBe("Undo");
    options.action.onClick();

    // The retired outbound switch is not sent back (CID-22): a member is an AI or a user, nothing more.
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      kind: "agent",
      member_id: "a1",
    });
  });
});

describe("member sessions (SESS-44)", () => {
  const at = "2026-10-28T14:05:00Z";
  it("an online AI with a parallel session lists principal and its labels; a single or offline one none", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    const withPi = makeMember({
      id: "mb-atlas",
      member_id: "a1",
      name: "Atlas",
      intelligence: atlas,
      online: true,
      sessions: [
        { label: "", harness: "claude", connected_at: at },
        { label: "pi-2", harness: "pi", connected_at: at },
      ],
    });
    const single = makeMember({
      id: "mb-solo",
      member_id: "a2",
      name: "Solo",
      intelligence: makeAI({ id: "a2", display_name: "Solo", handle: "solo" }),
      online: true,
      sessions: [{ label: "", harness: "claude", connected_at: at }],
    });
    const offline = makeMember({
      id: "mb-off",
      member_id: "a3",
      name: "Off",
      intelligence: makeAI({ id: "a3", display_name: "Off", handle: "off" }),
      online: false,
      sessions: [{ label: "wt", harness: "claude", connected_at: at }],
    });
    renderWithRouter(
      <MemberPanel
        roomId="r1"
        members={[withPi, single, offline]}
        canManage={false}
        loading={false}
      />,
    );

    const line = await screen.findByTestId("member-sessions-mb-atlas");
    expect(line).toHaveTextContent("principal");
    expect(line).toHaveTextContent(":pi-2");
    expect(screen.queryByTestId("member-sessions-mb-solo")).toBeNull();
    expect(screen.queryByTestId("member-sessions-mb-off")).toBeNull();
  });

  it("warns in the member card when a session runs an outdated client (SESS-63)", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    const outdated = makeMember({
      id: "mb-atlas",
      member_id: "a1",
      name: "Atlas",
      intelligence: atlas,
      online: true,
      sessions: [
        { label: "", harness: "claude", connected_at: at, outdated: true },
        { label: "claude-2", harness: "claude", connected_at: at },
      ],
    });
    renderWithRouter(
      <MemberPanel roomId="r1" members={[outdated]} canManage={false} loading={false} />,
    );

    const warning = await screen.findByTestId("session-outdated");
    expect(warning).toHaveTextContent(
      "Outdated client — run partyline update and reopen this session",
    );
    expect(warning).not.toHaveTextContent(":claude-2");
  });
});

describe("member principal (SESS-59, SESS-61)", () => {
  const at = "2026-10-28T14:05:00Z";
  const awayMember = makeMember({
    id: "mb-atlas",
    member_id: "a1",
    name: "Atlas",
    intelligence: atlas,
    online: true,
    principal_offline_since: "2026-10-28T14:00:00Z",
    sessions: [{ label: "pi-2", harness: "pi", connected_at: at, can_be_principal: true }],
  });
  const profile = (managed: boolean): IntelligenceProfile =>
    makeProfile({
      ...atlas,
      can_end_session: managed,
      ai: {
        owner: makeIntelligence(),
        folder: "/home/dev/atlas",
        folder_label: "~/atlas",
        app: "pi",
        online: true,
        sessions: [{ label: "pi-2", harness: "pi", connected_at: at, can_be_principal: true }],
        my_level: "manage",
      },
    });

  it("says the principal is offline and offers Make principal to whoever manages the AI", async () => {
    const { calls } = mockApi({
      "GET /me": json(200, makeMe()),
      "GET /intelligences/a1": json(200, profile(true)),
      "POST /agents/a1/principal": () => new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <MemberPanel roomId="r1" members={[awayMember]} canManage={false} loading={false} />,
    );
    expect(await screen.findByTestId("member-principal-offline-mb-atlas")).toHaveTextContent(
      /^Principal offline since/,
    );
    await user.click(
      await screen.findByRole("button", { name: "Make :pi-2 the principal session of Atlas" }),
    );
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ label: "pi-2" });
  });

  it("offers nothing to whoever does not manage the AI, but still says the principal is away", async () => {
    mockApi({ "GET /me": json(200, makeMe()), "GET /intelligences/a1": json(200, profile(false)) });
    renderWithRouter(
      <MemberPanel roomId="r1" members={[awayMember]} canManage={false} loading={false} />,
    );
    expect(await screen.findByTestId("member-principal-offline-mb-atlas")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("member-sessions-mb-atlas")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /Make :pi-2/ })).toBeNull();
  });
});

describe("member panel, loading and empty states", () => {
  it("shows a skeleton while the members load, for a group and for a DM alike", () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    const group = renderWithProviders(
      <MemberPanel roomId="r1" members={[]} canManage={false} loading />,
    );
    expect(group.container.querySelector('[data-slot="skeleton"]')).not.toBeNull();
    expect(screen.queryByRole("list")).toBeNull();
    group.unmount();
    const dm = renderWithProviders(
      <MemberPanel
        roomId="d1"
        members={[]}
        participants={[atlas, bob]}
        canManage={false}
        loading
      />,
    );
    expect(dm.container.querySelector('[data-slot="skeleton"]')).not.toBeNull();
    expect(screen.queryByRole("list")).toBeNull();
  });

  it("says so when a DM has nobody to show", async () => {
    mockApi({ "GET /me": json(200, makeMe()) });
    renderWithRouter(
      <MemberPanel roomId="d1" members={[]} participants={[]} canManage={false} loading={false} />,
    );
    expect(await screen.findByText("Nobody to show in this conversation.")).toBeInTheDocument();
  });
});

describe("member panel, DM participants (DM-02, WA-08)", () => {
  const at = "2026-10-28T14:05:00Z";
  const session = (label: string, harness = "claude"): SessionInfo => ({
    label,
    harness,
    connected_at: at,
  });
  const profileOf = (
    ai: ReturnType<typeof makeAI>,
    over: { online: boolean; sessions: SessionInfo[] },
  ): IntelligenceProfile =>
    makeProfile({
      ...ai,
      apps: [{ kind: "claude", label: "Claude Code" }],
      ai: {
        owner: makeMe().intelligence,
        folder: "",
        folder_label: "",
        app: "claude",
        ...over,
      },
    });

  function renderDM(
    participants = [makeMe().intelligence, atlas],
    api: Parameters<typeof mockApi>[0] = {},
  ) {
    mockApi({ "GET /me": json(200, makeMe()), ...api });
    return renderWithRouter(
      <MemberPanel
        roomId="d1"
        members={[]}
        participants={participants}
        canManage
        loading={false}
      />,
    );
  }

  it("lists the two participants with the same row as a group, read-only", async () => {
    renderDM(undefined, {
      "GET /intelligences/a1": json(
        200,
        profileOf(atlas, { online: true, sessions: [session("")] }),
      ),
    });
    const list = await screen.findByRole("list", { name: "Participants" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    const ai = within(screen.getByTestId("participant-a1"));
    expect(ai.getByTestId("intelligence-avatar")).toBeInTheDocument();
    expect(ai.getByText("@atlas")).toBeInTheDocument();
    expect(await ai.findByText("Online")).toBeInTheDocument();
    expect(ai.getByText("Claude Code")).toBeInTheDocument();
    const you = within(screen.getByTestId("participant-u1"));
    expect(you.getByText("You")).toBeInTheDocument();
    // Nobody adds, removes or configures anyone in a DM, even when the caller could manage.
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("shows the sessions of an online AI with a parallel one: principal · :pi-2", async () => {
    renderDM(undefined, {
      "GET /intelligences/a1": json(
        200,
        profileOf(atlas, { online: true, sessions: [session(""), session("pi-2", "pi")] }),
      ),
    });
    const line = await screen.findByTestId("participant-sessions-a1");
    expect(line).toHaveTextContent(/principal\s*·\s*:pi-2/);
  });

  it("shows none for an offline AI or an AI with its principal session alone", async () => {
    renderDM([atlas, makeAI({ id: "a2", display_name: "Solo", handle: "solo" })], {
      "GET /intelligences/a1": json(
        200,
        profileOf(atlas, { online: false, sessions: [session(""), session("pi-2", "pi")] }),
      ),
      "GET /intelligences/a2": json(
        200,
        profileOf(makeAI({ id: "a2", display_name: "Solo", handle: "solo" }), {
          online: true,
          sessions: [session("")],
        }),
      ),
    });
    expect(
      await within(await screen.findByTestId("participant-a1")).findByText("Offline"),
    ).toBeInTheDocument();
    await within(screen.getByTestId("participant-a2")).findByText("Online");
    expect(screen.queryByTestId("participant-sessions-a1")).toBeNull();
    expect(screen.queryByTestId("participant-sessions-a2")).toBeNull();
  });

  it("follows the live presence of the AI", async () => {
    renderDM(undefined, {
      "GET /intelligences/a1": json(
        200,
        profileOf(atlas, { online: true, sessions: [session(""), session("pi-2", "pi")] }),
      ),
    });
    await screen.findByTestId("participant-sessions-a1");
    act(() => useLiveStore.getState().setPresence("a1", { online: false }));
    expect(screen.queryByTestId("participant-sessions-a1")).toBeNull();
  });

  it("shows a person without status or sessions", async () => {
    renderDM([makeAI(), makePerson()], {
      "GET /intelligences/a1": json(
        200,
        profileOf(atlas, { online: true, sessions: [session("")] }),
      ),
    });
    const lia = within(await screen.findByTestId("participant-p1"));
    expect(lia.getByText("@lia")).toBeInTheDocument();
    expect(lia.queryByText(/line$/)).toBeNull();
  });
});
