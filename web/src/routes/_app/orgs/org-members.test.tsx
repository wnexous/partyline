import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import type { Organization, OrgMember } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeAIContact,
  makeChartPosition,
  makeContacts,
  makeIntelligence,
  makeInvitation,
  makeMe,
  makeOrg,
  makeOrgAIs,
  makeOrgChart,
  makeOrgMember,
} from "@/test/fixtures";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The members of an organization and who may invite and manage them (T42: OMEM-01, OMEM-03,
 * OMEM-04, OMEM-05, OMEM-06, POS-05, POS-06). Read through the route, with what the viewer may do
 * about each member as the server's member rows say it (CHART-03).
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const ana = makeIntelligence();
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const cora = makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" });
const dev = makeIntelligence({ id: "u4", display_name: "Dev Reis", handle: "dev" });
const eli = makeIntelligence({ id: "u5", display_name: "Eli Costa", handle: "eli" });
const worker = makeAI({ id: "a1", display_name: "Worker 1", handle: "w1" });
const sentinel = makeAI({ id: "a2", display_name: "Sentinel", handle: "sentinel" });

// Acme: Admin > Manager > Lead developer > Worktrees. Ana (admin) holds Admin, Bob (member) Manager.
const POSITIONS = [
  { id: "pos1", name: "Admin" },
  { id: "pos2", name: "Manager", parent_id: "pos1" },
  { id: "pos3", name: "Lead developer", parent_id: "pos2" },
  { id: "pos4", name: "Worktrees", parent_id: "pos3" },
];

type Flags = Partial<Pick<OrgMember, "can_delete" | "delete_effect" | "can_move">>;

/** The rows `GET /orgs/o1/members` serves, with what the viewer may do about each one. */
const rows = (flags: Record<string, Flags> = {}): OrgMember[] =>
  [
    makeOrgMember({
      intelligence: ana,
      permission: "admin",
      position_id: "pos1",
      position_name: "Admin",
    }),
    makeOrgMember({
      intelligence: bob,
      permission: "member",
      position_id: "pos2",
      position_name: "Manager",
    }),
    makeOrgMember({ intelligence: cora, permission: "member" }),
    makeOrgMember({ intelligence: dev, permission: "guest", invited_by: bob }),
    makeOrgMember({
      intelligence: worker,
      permission: "guest",
      position_id: "pos4",
      position_name: "Worktrees",
      invited_by: bob,
    }),
    makeOrgMember({ intelligence: sentinel, permission: "member" }),
  ].map((m) => ({
    ...m,
    // An AI of the organization is deleted for good, anyone else is removed from it.
    delete_effect: m.intelligence.kind === "ai" ? ("delete" as const) : ("remove" as const),
    ...flags[m.intelligence.id],
  }));

const members = rows();

const MAY = { can_delete: true, can_move: true } as const;
/** An admin may delete and move everyone but themselves. */
const adminRows = rows({ u2: MAY, u3: MAY, u4: MAY, a1: MAY, a2: MAY });
/** Bob, a manager, answers for the guests he invited, and for nobody else. */
const managerRows = rows({ u4: MAY, a1: MAY });
/** A member or a guest may do nothing about anyone. */
const plainRows = rows();

/** The chart gives the positions the dialogs offer. */
const chart = (admin: boolean) =>
  makeOrgChart({
    viewer: { admin },
    positions: POSITIONS.map((p) => makeChartPosition(p)),
  });

const acmeAs = (permission: Organization["permission"], position_id?: string) =>
  makeOrg({
    id: "o1",
    name: "Acme",
    slug: "acme",
    permission,
    ...(position_id ? { position_id } : {}),
  });

interface World {
  org: Organization;
  me: ReturnType<typeof makeMe>;
  rows: OrgMember[];
  admin: boolean;
  over: Parameters<typeof mockApi>[0];
}

const asAdmin = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("admin", "pos1"),
  me: makeMe(),
  rows: adminRows,
  admin: true,
  over,
});
const asManager = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("member", "pos2"),
  me: makeMe({ id: "u2", intelligence: bob, display_name: "Bob Lima", username: "bob" }),
  rows: managerRows,
  admin: false,
  over,
});
const asMember = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("member"),
  me: makeMe({ id: "u3", intelligence: cora, display_name: "Cora Dias", username: "cora" }),
  rows: plainRows,
  admin: false,
  over,
});
const asGuest = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("guest"),
  me: makeMe({ id: "u4", intelligence: dev, display_name: "Dev Reis", username: "dev" }),
  rows: plainRows,
  admin: false,
  over,
});

const contacts = makeContacts({
  me: ana,
  org_ais: [
    makeOrgAIs({
      ais: [
        makeAIContact({ intelligence: worker }),
        makeAIContact({ intelligence: sentinel }),
        makeAIContact({
          intelligence: makeAI({ id: "a9", display_name: "Notes", handle: "notes" }),
        }),
      ],
    }),
  ],
  users: [bob, cora, dev, eli],
});

function api(world: World) {
  return mockApi({
    "GET /me": json(200, world.me),
    "GET /orgs": json(200, { items: [world.org] }),
    "GET /orgs/o1": json(200, world.org),
    "GET /orgs/o1/members": json(200, { items: world.rows }),
    "GET /orgs/o1/chart": json(200, chart(world.admin)),
    "GET /orgs/o1/invitations": json(200, { items: [] }),
    "GET /contacts": json(200, contacts),
    "GET /conversations": json(200, { items: [] }),
    ...world.over,
  });
}

async function open(world: World) {
  const mocks = api(world);
  const user = userEvent.setup();
  const view = renderRoute("/orgs/o1/members");
  await screen.findByRole("heading", { level: 1, name: "Members" });
  const table = await screen.findByRole("table", { name: "Members of Acme" });
  return { ...mocks, ...view, user, table };
}
/** The row of a member: the one whose row header (the identity) names them. */
const row = (table: HTMLElement, name: string) => {
  const found = within(table)
    .getAllByRole("row")
    .find((r) => within(r).queryByRole("rowheader")?.textContent?.includes(name));
  if (!found) throw new Error(`no row for ${name}`);
  return found;
};
const labelled = (el: HTMLElement) =>
  within(el)
    .queryAllByRole("button")
    .map((b) => b.textContent);

beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null, presence: {} });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("the members table (OMEM-03)", () => {
  it("lists every member with who they are, what they are in the organization, their position and, for a guest, who invited them", async () => {
    const { table } = await open(asMember());
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(6);
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(["Member", "Permission", "Position", "Invited by", "Actions"]);
    // Admins first, then members, then guests; by name inside each.
    expect(rows.map((r) => within(r).getByRole("rowheader").textContent)).toEqual([
      expect.stringContaining("Ana Souza"),
      expect.stringContaining("Bob Lima"),
      expect.stringContaining("Cora Dias"),
      expect.stringContaining("Sentinel"),
      expect.stringContaining("Dev Reis"),
      expect.stringContaining("Worker 1"),
    ]);

    const bobRow = row(table, "Bob Lima");
    expect(within(bobRow).getByText("@bob")).toBeInTheDocument();
    expect(within(bobRow).getByText("Member")).toBeInTheDocument();
    expect(within(bobRow).getByText("Manager")).toBeInTheDocument();

    const workerRow = row(table, "Worker 1");
    expect(within(workerRow).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(workerRow).getByText("@w1")).toBeInTheDocument();
    expect(within(workerRow).getByText("Guest")).toBeInTheDocument();
    expect(within(workerRow).getByText("Worktrees")).toBeInTheDocument();
    expect(within(workerRow).getByText("Invited by Bob Lima")).toBeInTheDocument();

    expect(within(row(table, "Dev Reis")).getByText("Invited by Bob Lima")).toBeInTheDocument();
    // Ana is an Admin by permission and holds the Admin position: the first cell is the permission.
    expect(within(row(table, "Ana Souza")).getAllByRole("cell")[0]).toHaveTextContent("Admin");
    // Only a guest has an inviter to show.
    expect(within(bobRow).queryByText(/Invited by/)).toBeNull();
  });

  it("marks the viewer's own row", async () => {
    const { table } = await open(asMember());
    expect(within(row(table, "Cora Dias")).getByText("You")).toBeInTheDocument();
    expect(within(row(table, "Bob Lima")).queryByText("You")).toBeNull();
  });

  it("is open to a guest, who sees the members (CHART-02)", async () => {
    const { table } = await open(asGuest());
    expect(within(table).getAllByRole("row")).toHaveLength(7);
  });

  it("says so, and offers to try again, when the members cannot be read", async () => {
    api(asMember({ "GET /orgs/o1/members": apiError(500, "internal", "boom") }));
    renderRoute("/orgs/o1/members");
    const text = await screen.findByText("Couldn’t load the members.");
    expect(
      within(text.closest("[role=alert]") as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    ).toBeInTheDocument();
  });

  it("follows the organization live: a member who joined shows without a reload", async () => {
    const { queryClient } = await open(asMember());
    const joined = [...members, makeOrgMember({ intelligence: eli, permission: "member" })];
    mockApi({
      "GET /me": json(200, asMember().me),
      "GET /orgs": json(200, { items: [asMember().org] }),
      "GET /orgs/o1": json(200, asMember().org),
      "GET /orgs/o1/members": json(200, { items: joined }),
      "GET /orgs/o1/chart": json(200, chart(false)),
      "GET /conversations": json(200, { items: [] }),
    });
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "access.changed",
        data: { resource_kind: "organization", resource_id: "o1" },
      }),
    );
    expect(await screen.findByText("Eli Costa")).toBeInTheDocument();
  });
});

describe("who may invite, and as what (OMEM-01, OMEM-04)", () => {
  const dialog = () => screen.findByRole("dialog", { name: "Invite to Acme" });

  it.each([
    ["a member", asMember],
    ["a guest", asGuest],
  ])(
    "offers %s no permission and no position, and sends the invitation as a guest",
    async (_, world) => {
      const { calls, user } = await open(
        world({
          "POST /orgs/o1/invitations": json(
            201,
            makeInvitation({ invitee: eli, permission: "guest" }),
          ),
        }),
      );
      await user.click(screen.getByRole("button", { name: "Invite" }));
      const d = await dialog();
      expect(within(d).queryByRole("combobox", { name: "Permission" })).toBeNull();
      expect(within(d).queryByRole("combobox", { name: "Position" })).toBeNull();
      expect(
        within(d).getByText(
          "You can invite guests. A guest is your responsibility: you, and the identities above you, may manage them and remove them from Acme.",
        ),
      ).toBeInTheDocument();

      await user.type(within(d).getByRole("textbox", { name: "Handle" }), "@Eli ");
      await user.click(within(d).getByRole("button", { name: "Send invitation" }));
      await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
      expect(calls.find((c) => c.method === "POST")?.body).toEqual({
        handle: "eli",
        permission: "guest",
      });
      expect(await screen.findByText("Invitation sent to Eli Costa")).toBeInTheDocument();
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    },
  );

  it("lets an admin pick any permission and a position, and sends both", async () => {
    const { calls, user } = await open(
      asAdmin({
        "POST /orgs/o1/invitations": json(
          201,
          makeInvitation({ invitee: eli, permission: "member", position_id: "pos4" }),
        ),
      }),
    );
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    expect(
      within(d).getByRole("combobox", { name: "Permission" }).querySelectorAll("option"),
    ).toHaveLength(3);
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "eli");
    await user.selectOptions(within(d).getByRole("combobox", { name: "Permission" }), "member");
    const position = within(d).getByRole("combobox", { name: "Position" });
    expect(
      within(position)
        .getAllByRole("option")
        .map((o) => o.textContent?.trim()),
    ).toEqual(["No position", "Admin", "Manager", "Lead developer", "Worktrees"]);
    await user.selectOptions(position, "pos4");
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      handle: "eli",
      permission: "member",
      position_id: "pos4",
    });
  });

  it("sends no position when none is picked, and starts the admin on a member", async () => {
    const { calls, user } = await open(
      asAdmin({ "POST /orgs/o1/invitations": json(201, makeInvitation({ invitee: eli })) }),
    );
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    expect(within(d).getByRole("combobox", { name: "Permission" })).toHaveValue("member");
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "eli");
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      handle: "eli",
      permission: "member",
    });
  });

  it("can invite an admin and a guest too", async () => {
    const { calls, user } = await open(
      asAdmin({ "POST /orgs/o1/invitations": json(201, makeInvitation({ invitee: eli })) }),
    );
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "eli");
    await user.selectOptions(within(d).getByRole("combobox", { name: "Permission" }), "guest");
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toMatchObject({ permission: "guest" });
  });

  it("asks for a handle before asking the server", async () => {
    const { calls, user } = await open(asMember());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    expect(await within(d).findByText("Required")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("suggests the users and AIs the viewer can see who are not members yet, and fills the handle on a pick", async () => {
    const { user } = await open(asMember());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    const field = within(d).getByRole("textbox", { name: "Handle" });
    await user.type(field, "e");
    // Eli is a user who is not a member; Notes is an AI the viewer sees; Bob, Cora and Dev are members.
    const suggestions = within(d).getByRole("list", { name: "Suggestions" });
    expect(
      within(suggestions)
        .getAllByRole("button")
        .map((b) => b.textContent),
    ).toEqual([expect.stringContaining("Eli Costa"), expect.stringContaining("Notes")]);
    await user.click(within(suggestions).getByRole("button", { name: /Eli Costa/ }));
    expect(field).toHaveValue("@eli");
    expect(within(d).queryByRole("list", { name: "Suggestions" })).toBeNull();
  });

  it.each([
    ["a member", "bob"],
    ["a guest", "dev"],
    ["an AI member", "sentinel"],
    ["the viewer", "cora"],
  ])("never suggests %s", async (_, typed) => {
    const { user } = await open(asMember());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), typed);
    expect(within(d).queryByRole("list", { name: "Suggestions" })).toBeNull();
  });

  describe("the refusals", () => {
    async function submit(world: World, handle = "eli") {
      const mocks = await open(world);
      await mocks.user.click(screen.getByRole("button", { name: "Invite" }));
      const d = await dialog();
      await mocks.user.type(within(d).getByRole("textbox", { name: "Handle" }), handle);
      await mocks.user.click(within(d).getByRole("button", { name: "Send invitation" }));
      return { ...mocks, d };
    }

    it("says who is already a member (409 already_member), under the handle", async () => {
      const { d } = await submit(
        asMember({
          "POST /orgs/o1/invitations": apiError(409, "already_member", "already a member"),
        }),
        "bob",
      );
      expect(await within(d).findByText("@bob is already a member of Acme.")).toBeInTheDocument();
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it("says no one has that handle (404 contact_not_found)", async () => {
      const { d } = await submit(
        asMember({ "POST /orgs/o1/invitations": apiError(404, "contact_not_found", "not found") }),
        "nobody",
      );
      expect(await within(d).findByText("No one has that handle.")).toBeInTheDocument();
    });

    it("says there were too many invitations (429)", async () => {
      const { d } = await submit(
        asMember({ "POST /orgs/o1/invitations": apiError(429, "rate_limited", "slow down") }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Too many invitations in the last hour. Try again later.",
      );
    });

    it("says a member can only invite guests (403)", async () => {
      const { d } = await submit(
        asMember({ "POST /orgs/o1/invitations": apiError(403, "forbidden", "forbidden") }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent("You can only invite guests.");
    });

    it("shows the server's reason for a field (422)", async () => {
      const { d } = await submit(
        asAdmin({
          "POST /orgs/o1/invitations": apiError(422, "invalid", "invalid", {
            position_id: "is not a position of this organization",
          }),
        }),
      );
      expect(
        await within(d).findByText("is not a position of this organization"),
      ).toBeInTheDocument();
    });

    it("says so when the server cannot be reached", async () => {
      const { d } = await submit(
        asMember({
          "POST /orgs/o1/invitations": () => Promise.reject(new TypeError("offline")) as never,
        }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Can’t reach the server. Check your connection and try again.",
      );
    });
  });

  it("starts empty every time it opens", async () => {
    const { user } = await open(asMember());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    let d = await dialog();
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "eli");
    await user.click(within(d).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    d = await dialog();
    expect(within(d).getByRole("textbox", { name: "Handle" })).toHaveValue("");
  });
});

describe("changing a permission (OMEM-04, OMEM-05)", () => {
  it("lets an admin change it from the row, sending only the permission", async () => {
    const { calls, user, table } = await open(
      asAdmin({ "PATCH /orgs/o1/members/u3": json(200, members[2]) }),
    );
    const select = within(row(table, "Cora Dias")).getByRole("combobox", {
      name: "Permission of Cora Dias",
    });
    expect(select).toHaveValue("member");
    await user.selectOptions(select, "admin");
    expect(await screen.findByText("Cora Dias is now admin")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ permission: "admin" });
    expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/orgs/o1/members/u3");
  });

  it("offers a member, a guest and a manager only the permission as text", async () => {
    for (const world of [asMember, asGuest, asManager]) {
      const { table, unmount } = await open(world());
      expect(within(table).queryByRole("combobox")).toBeNull();
      unmount();
    }
  });

  it("stops offering the admin's controls to an admin who steps down", async () => {
    let org = acmeAs("admin", "pos1");
    const { user, table } = await open(
      asAdmin({
        "GET /orgs/o1": () => json(200, org),
        "PATCH /orgs/o1/members/u1": () => {
          org = acmeAs("member", "pos1");
          return json(200, members[0]);
        },
      }),
    );
    const select = within(row(table, "Ana Souza")).getByRole("combobox", {
      name: "Permission of Ana Souza",
    });
    await user.selectOptions(select, "member");
    await waitFor(() => expect(within(table).queryByRole("combobox")).toBeNull());
    expect(await screen.findByText("Ana Souza is now member")).toBeInTheDocument();
  });

  it("tells the only admin to make another member an admin first, and keeps the old permission (409 last_admin)", async () => {
    const { user, table } = await open(
      asAdmin({ "PATCH /orgs/o1/members/u1": apiError(409, "last_admin", "last admin") }),
    );
    const select = within(row(table, "Ana Souza")).getByRole("combobox", {
      name: "Permission of Ana Souza",
    });
    await user.selectOptions(select, "member");
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Ana Souza is the only admin of Acme. Make another member an admin first.",
    );
    await waitFor(() => expect(select).toHaveValue("admin"));
  });

  it("says only admins change permissions when the server refuses (403), and when it fails otherwise", async () => {
    const { user, table } = await open(
      asAdmin({ "PATCH /orgs/o1/members/u3": apiError(403, "forbidden", "forbidden") }),
    );
    await user.selectOptions(
      within(row(table, "Cora Dias")).getByRole("combobox", { name: "Permission of Cora Dias" }),
      "guest",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only admins of Acme change permissions.",
    );
  });
});

describe("what the viewer may do about each member (POS-04, POS-05, POS-06)", () => {
  it("shows an admin Move on everyone else, Delete for an AI of the organization and Remove from Acme for anyone else", async () => {
    const { table } = await open(asAdmin());
    expect(labelled(row(table, "Ana Souza"))).toEqual([]);
    expect(labelled(row(table, "Bob Lima"))).toEqual(["Move", "Remove from Acme"]);
    expect(labelled(row(table, "Cora Dias"))).toEqual(["Move", "Remove from Acme"]);
    expect(labelled(row(table, "Dev Reis"))).toEqual(["Move", "Remove from Acme"]);
    expect(labelled(row(table, "Worker 1"))).toEqual(["Move", "Delete"]);
    expect(labelled(row(table, "Sentinel"))).toEqual(["Move", "Delete"]);
  });

  it("shows a manager Move only on those below them and Delete or Remove only on the guests they answer for", async () => {
    const { table } = await open(asManager());
    expect(labelled(row(table, "Dev Reis"))).toEqual(["Move", "Remove from Acme"]);
    expect(labelled(row(table, "Worker 1"))).toEqual(["Move", "Delete"]);
    // Nothing for their peers, for those above them, nor for themselves.
    for (const who of ["Ana Souza", "Bob Lima", "Cora Dias", "Sentinel"]) {
      expect(labelled(row(table, who))).toEqual([]);
    }
  });

  it.each([
    ["a member", asMember],
    ["a guest", asGuest],
  ])("shows %s no action at all", async (_, world) => {
    const { table } = await open(world());
    expect(within(table).queryByRole("button", { name: /Move|Delete|Remove/ })).toBeNull();
  });

  it("still shows the actions when the chart cannot be read: they come with the members", async () => {
    api(asAdmin({ "GET /orgs/o1/chart": apiError(500, "internal", "boom") }));
    renderRoute("/orgs/o1/members");
    const table = await screen.findByRole("table", { name: "Members of Acme" });
    expect(labelled(row(table, "Cora Dias"))).toEqual(["Move", "Remove from Acme"]);
  });

  it("offers an admin only 'No position' to move to while the chart that holds the positions cannot be read", async () => {
    const { user, table } = await open(
      asAdmin({ "GET /orgs/o1/chart": apiError(500, "internal", "boom") }),
    );
    await user.click(within(row(table, "Cora Dias")).getByRole("button", { name: "Move" }));
    const d = await screen.findByRole("dialog", { name: "Move Cora Dias" });
    expect(
      within(within(d).getByRole("combobox", { name: "Position" }))
        .getAllByRole("option")
        .map((o) => o.textContent?.trim()),
    ).toEqual(["No position"]);
  });

  describe("removing someone from the organization (POS-06)", () => {
    it("asks first, saying what stays, then removes them by id and refreshes the table", async () => {
      let list = adminRows;
      const { calls, user, table } = await open(
        asAdmin({
          "GET /orgs/o1/members": () => json(200, { items: list }),
          "DELETE /orgs/o1/members/u3": () => {
            list = adminRows.filter((m) => m.intelligence.id !== "u3");
            return new Response(null, { status: 204 });
          },
        }),
      );
      await user.click(
        within(row(table, "Cora Dias")).getByRole("button", { name: "Remove from Acme" }),
      );
      const d = await screen.findByRole("alertdialog", { name: "Remove Cora Dias from Acme?" });
      expect(
        within(d).getByText(
          "Cora Dias leaves the groups of Acme and loses their position there. Removing someone only takes them out of Acme: their account and their other organizations stay.",
        ),
      ).toBeInTheDocument();
      await user.click(within(d).getByRole("button", { name: "Remove from Acme" }));
      await waitFor(() =>
        expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
          "/orgs/o1/members/u3",
        ]),
      );
      await waitFor(() => expect(screen.queryByText("Cora Dias")).toBeNull());
    });

    it("shows last_admin inline and keeps the dialog open (409)", async () => {
      const { user, table } = await open(
        asAdmin({ "DELETE /orgs/o1/members/u2": apiError(409, "last_admin", "last admin") }),
      );
      await user.click(
        within(row(table, "Bob Lima")).getByRole("button", { name: "Remove from Acme" }),
      );
      const d = await screen.findByRole("alertdialog");
      await user.click(within(d).getByRole("button", { name: "Remove from Acme" }));
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Bob Lima is the only admin of Acme. Make another member an admin first.",
      );
    });

    it("shows home_organization inline: an AI whose home it is is deleted by an admin (409)", async () => {
      const { user, table } = await open(
        asAdmin({
          "DELETE /orgs/o1/members/u3": apiError(409, "home_organization", "home organization"),
        }),
      );
      await user.click(
        within(row(table, "Cora Dias")).getByRole("button", { name: "Remove from Acme" }),
      );
      const d = await screen.findByRole("alertdialog");
      await user.click(within(d).getByRole("button", { name: "Remove from Acme" }));
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Cora Dias belongs to Acme. Ask an admin to delete it.",
      );
    });
  });

  describe("deleting an AI of the organization (POS-06)", () => {
    it("asks first, then deletes it for good and refreshes the table", async () => {
      let list = managerRows;
      const { calls, user, table } = await open(
        asManager({
          "GET /orgs/o1/members": () => json(200, { items: list }),
          "DELETE /agents/a1": () => {
            list = managerRows.filter((m) => m.intelligence.id !== "a1");
            return new Response(null, { status: 204 });
          },
        }),
      );
      await user.click(within(row(table, "Worker 1")).getByRole("button", { name: "Delete" }));
      const d = await screen.findByRole("alertdialog", { name: "Delete Worker 1?" });
      await user.click(within(d).getByRole("button", { name: "Delete AI" }));
      await waitFor(() =>
        expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
          "/agents/a1",
        ]),
      );
      await waitFor(() => expect(screen.queryByText("Worker 1")).toBeNull());
    });
  });

  describe("moving someone between positions (POS-04)", () => {
    it("offers a manager only the positions below theirs, never their own level or none; where the member is now is shown, not offered", async () => {
      const { user, table } = await open(asManager());
      await user.click(within(row(table, "Dev Reis")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Dev Reis" });
      const select = within(d).getByRole("combobox", { name: "Position" });
      const options = within(select).getAllByRole("option");
      expect(options.map((o) => o.textContent?.trim())).toEqual([
        "No position",
        "Lead developer",
        "Worktrees",
      ]);
      // Dev holds none now: that is shown as the current choice and cannot be picked again.
      expect(options[0]).toBeDisabled();
      expect(select).toHaveValue("");
    });

    it("offers an admin every position and the way to hold none, starting on where the member is", async () => {
      const { user, table } = await open(asAdmin());
      await user.click(within(row(table, "Worker 1")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Worker 1" });
      const select = within(d).getByRole("combobox", { name: "Position" });
      expect(
        within(select)
          .getAllByRole("option")
          .map((o) => o.textContent?.trim()),
      ).toEqual(["No position", "Admin", "Manager", "Lead developer", "Worktrees"]);
      expect(select).toHaveValue("pos4");
      for (const o of within(select).getAllByRole("option")) expect(o).toBeEnabled();
    });

    it("moves them: the request carries the position, the table shows it and a toast says so", async () => {
      let list = managerRows;
      const { calls, user, table } = await open(
        asManager({
          "GET /orgs/o1/members": () => json(200, { items: list }),
          "PATCH /orgs/o1/members/u4": () => {
            list = managerRows.map((m) =>
              m.intelligence.id === "u4"
                ? { ...m, position_id: "pos3", position_name: "Lead developer" }
                : m,
            );
            return json(200, list[3]);
          },
        }),
      );
      await user.click(within(row(table, "Dev Reis")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Dev Reis" });
      await user.selectOptions(within(d).getByRole("combobox", { name: "Position" }), "pos3");
      await user.click(within(d).getByRole("button", { name: "Move" }));
      expect(await screen.findByText("Dev Reis moved to Lead developer")).toBeInTheDocument();
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ position_id: "pos3" });
      await waitFor(() =>
        expect(within(row(table, "Dev Reis")).getByText("Lead developer")).toBeInTheDocument(),
      );
    });

    it("takes the position away from the member an admin picks 'No position' for", async () => {
      const { calls, user, table } = await open(
        asAdmin({ "PATCH /orgs/o1/members/a1": json(200, members[4]) }),
      );
      // Worker 1 holds Worktrees; the admin clears it.
      await user.click(within(row(table, "Worker 1")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Worker 1" });
      await user.selectOptions(within(d).getByRole("combobox", { name: "Position" }), "");
      await user.click(within(d).getByRole("button", { name: "Move" }));
      await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
      expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/orgs/o1/members/a1");
      expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ position_id: "" });
      expect(await screen.findByText("Worker 1 no longer holds a position")).toBeInTheDocument();
    });

    it("sends nothing when the form is submitted without a different position", async () => {
      const { calls, user, table } = await open(asAdmin());
      await user.click(within(row(table, "Worker 1")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Worker 1" });
      fireEvent.submit(
        within(d).getByRole("combobox", { name: "Position" }).closest("form") as HTMLFormElement,
      );
      await new Promise((r) => setTimeout(r, 50));
      expect(calls.some((c) => c.method === "PATCH")).toBe(false);
    });

    it("keeps Move off until a different position is picked", async () => {
      const { user, table } = await open(asAdmin());
      await user.click(within(row(table, "Worker 1")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Worker 1" });
      expect(within(d).getByRole("button", { name: "Move" })).toBeDisabled();
      await user.selectOptions(within(d).getByRole("combobox", { name: "Position" }), "pos3");
      expect(within(d).getByRole("button", { name: "Move" })).toBeEnabled();
      await user.selectOptions(within(d).getByRole("combobox", { name: "Position" }), "pos4");
      expect(within(d).getByRole("button", { name: "Move" })).toBeDisabled();
    });

    it("says a manager can only move people below their own position when the server refuses (403)", async () => {
      const { user, table } = await open(
        asManager({ "PATCH /orgs/o1/members/u4": apiError(403, "forbidden", "forbidden") }),
      );
      await user.click(within(row(table, "Dev Reis")).getByRole("button", { name: "Move" }));
      const d = await screen.findByRole("dialog", { name: "Move Dev Reis" });
      await user.selectOptions(within(d).getByRole("combobox", { name: "Position" }), "pos3");
      await user.click(within(d).getByRole("button", { name: "Move" }));
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "You can only move people to a position below yours.",
      );
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
  });
});

describe("the invitations still waiting (OMEM-01, OMEM-07)", () => {
  const pending = makeInvitation({
    id: "i1",
    invitee: eli,
    permission: "guest",
    invited_by: ana,
    expires_at: "2026-11-03T12:00:00Z",
  });

  it("lists them for an admin, and revokes one", async () => {
    let items = [pending];
    const { calls, user } = await open(
      asAdmin({
        "GET /orgs/o1/invitations": () => json(200, { items }),
        "DELETE /orgs/o1/invitations/i1": () => {
          items = [];
          return new Response(null, { status: 204 });
        },
      }),
    );
    const list = await screen.findByRole("list", { name: "Pending invitations of Acme" });
    const item = within(list).getByRole("listitem", { name: "Eli Costa" });
    expect(within(item).getByText("Guest")).toBeInTheDocument();
    expect(within(item).getByText("Invited by Ana Souza")).toBeInTheDocument();
    await user.click(within(item).getByRole("button", { name: "Revoke" }));
    expect(await screen.findByText("Invitation to Eli Costa revoked")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/orgs/o1/invitations/i1",
    ]);
    await waitFor(() =>
      expect(screen.queryByRole("list", { name: "Pending invitations of Acme" })).toBeNull(),
    );
  });

  it("shows the position an invitation carries", async () => {
    await open(
      asAdmin({
        "GET /orgs/o1/invitations": json(200, {
          items: [
            { ...pending, permission: "member", position_id: "pos4", position_name: "Worktrees" },
          ],
        }),
      }),
    );
    const item = await screen.findByRole("listitem", { name: "Eli Costa" });
    expect(within(item).getByText("Worktrees")).toBeInTheDocument();
  });

  it("is not shown to, nor asked for by, anyone but an admin", async () => {
    const { calls } = await open(
      asManager({ "GET /orgs/o1/invitations": json(200, { items: [pending] }) }),
    );
    expect(screen.queryByRole("list", { name: "Pending invitations of Acme" })).toBeNull();
    expect(calls.some((c) => c.path === "/orgs/o1/invitations")).toBe(false);
  });

  it("keeps the invitation, and says so, when it could not be revoked", async () => {
    const { user } = await open(
      asAdmin({
        "GET /orgs/o1/invitations": json(200, { items: [pending] }),
        "DELETE /orgs/o1/invitations/i1": apiError(500, "internal", "boom"),
      }),
    );
    const item = await screen.findByRole("listitem", { name: "Eli Costa" });
    await user.click(within(item).getByRole("button", { name: "Revoke" }));
    expect(
      await screen.findByText("Couldn’t revoke the invitation to Eli Costa. Try again."),
    ).toBeInTheDocument();
    expect(screen.getByRole("listitem", { name: "Eli Costa" })).toBeInTheDocument();
  });

  it("shows a new invitation in the list after one is sent", async () => {
    let items: ReturnType<typeof makeInvitation>[] = [];
    const { user } = await open(
      asAdmin({
        "GET /orgs/o1/invitations": () => json(200, { items }),
        "POST /orgs/o1/invitations": () => {
          items = [pending];
          return json(201, pending);
        },
      }),
    );
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await screen.findByRole("dialog", { name: "Invite to Acme" });
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "eli");
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    expect(await screen.findByRole("listitem", { name: "Eli Costa" })).toBeInTheDocument();
  });
});

describe("in Portuguese and for assistive technology", () => {
  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    const world = asAdmin({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    api(world);
    const user = userEvent.setup();
    renderRoute("/orgs/o1/members");
    expect(await screen.findByRole("heading", { level: 1, name: "Membros" })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Membros de Acme" });
    expect(labelled(row(table, "Cora Dias"))).toEqual(["Mover", "Remover de Acme"]);
    expect(labelled(row(table, "Worker 1"))).toEqual(["Mover", "Apagar"]);
    expect(within(row(table, "Dev Reis")).getByText("Convidado por Bob Lima")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Convidar" }));
    expect(await screen.findByRole("dialog", { name: "Convidar para Acme" })).toBeInTheDocument();
  });

  it("has no axe violations: the table for each part, the dialogs, and in Portuguese", async () => {
    for (const world of [asAdmin, asManager, asMember, asGuest]) {
      const { container, unmount } = await open(world());
      expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
      unmount();
    }

    const admin = await open(
      asAdmin({
        "GET /orgs/o1/invitations": json(200, {
          items: [makeInvitation({ id: "i1", invitee: eli, permission: "guest", invited_by: ana })],
        }),
      }),
    );
    await screen.findByRole("list", { name: "Pending invitations of Acme" });
    expect(await axe(admin.container, AXE_OPTIONS)).toHaveNoViolations();
    await admin.user.click(screen.getByRole("button", { name: "Invite" }));
    expect(
      await axe(await screen.findByRole("dialog", { name: "Invite to Acme" }), AXE_OPTIONS),
    ).toHaveNoViolations();
    await admin.user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await admin.user.click(
      within(row(admin.table, "Dev Reis")).getByRole("button", { name: "Move" }),
    );
    expect(
      await axe(await screen.findByRole("dialog", { name: "Move Dev Reis" }), AXE_OPTIONS),
    ).toHaveNoViolations();
    admin.unmount();

    const guest = await open(asGuest());
    await guest.user.click(screen.getByRole("button", { name: "Invite" }));
    expect(
      await axe(await screen.findByRole("dialog", { name: "Invite to Acme" }), AXE_OPTIONS),
    ).toHaveNoViolations();
    guest.unmount();

    useLiveStore.setState({ locale: "pt-BR" });
    api(asAdmin({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) }));
    const pt = renderRoute("/orgs/o1/members");
    await screen.findByRole("table", { name: "Membros de Acme" });
    expect(await axe(pt.container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
