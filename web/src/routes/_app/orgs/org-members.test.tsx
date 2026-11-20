import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { handleLiveEvent } from "@/app/live/dispatch";
import type { Organization, OrgMember } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAI,
  makeAIContact,
  makeContacts,
  makeIntelligence,
  makeInvitation,
  makeMe,
  makeOrg,
  makeOrgAIs,
  makeOrgMember,
} from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The members of an organization and who may invite and remove them (T42: OMEM-01, OMEM-03,
 * OMEM-04, OMEM-05, OMEM-06, POS-06, SR2-01, SR2-12). Read through the route, with what the viewer
 * may do about each member as the server's member rows say it.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const ana = makeIntelligence();
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const cora = makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" });
const dev = makeIntelligence({ id: "u4", display_name: "Dev Reis", handle: "dev" });
const eli = makeIntelligence({ id: "u5", display_name: "Eli Costa", handle: "eli" });
const worker = makeAI({ id: "a1", display_name: "Worker 1", handle: "w1" });
const sentinel = makeAI({ id: "a2", display_name: "Sentinel", handle: "sentinel" });

type Flags = Partial<Pick<OrgMember, "can_delete" | "delete_effect">>;

/** The rows `GET /orgs/o1/members` serves, with what the viewer may do about each one. */
const rows = (flags: Record<string, Flags> = {}): OrgMember[] =>
  [
    makeOrgMember({ intelligence: ana, permission: "admin" }),
    makeOrgMember({ intelligence: bob, permission: "member" }),
    makeOrgMember({ intelligence: cora, permission: "member" }),
    makeOrgMember({ intelligence: dev, permission: "member" }),
    makeOrgMember({ intelligence: worker, permission: "member" }),
    makeOrgMember({ intelligence: sentinel, permission: "member" }),
  ].map((m) => ({
    ...m,
    // An AI of the organization is deleted for good, anyone else is removed from it.
    delete_effect: m.intelligence.kind === "ai" ? ("delete" as const) : ("remove" as const),
    ...flags[m.intelligence.id],
  }));

const members = rows();

const MAY = { can_delete: true } as const;
/** An admin may delete or remove everyone but themselves (SR2-12). */
const adminRows = rows({ u2: MAY, u3: MAY, u4: MAY, a1: MAY, a2: MAY });
/** Bob, a member, hosts Worker 1: he may delete it, and nobody else (SR2-12). */
const hostRows = rows({ a1: MAY });
/** A member may do nothing about anyone. */
const plainRows = rows();

const acmeAs = (permission: Organization["permission"]) =>
  makeOrg({ id: "o1", name: "Acme", slug: "acme", permission });

interface World {
  org: Organization;
  me: ReturnType<typeof makeMe>;
  rows: OrgMember[];
  over: Parameters<typeof mockApi>[0];
}

const asAdmin = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("admin"),
  me: makeMe(),
  rows: adminRows,
  over,
});
const asHost = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("member"),
  me: makeMe({ id: "u2", intelligence: bob, display_name: "Bob Lima", username: "bob" }),
  rows: hostRows,
  over,
});
const asMember = (over: Parameters<typeof mockApi>[0] = {}): World => ({
  org: acmeAs("member"),
  me: makeMe({ id: "u3", intelligence: cora, display_name: "Cora Dias", username: "cora" }),
  rows: plainRows,
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
  it("SR2-04 lists every member with who they are and what they are in the organization, and no position", async () => {
    const { table } = await open(asMember());
    const rows = within(table).getAllByRole("row").slice(1);
    expect(rows).toHaveLength(6);
    expect(
      within(table)
        .getAllByRole("columnheader")
        .map((h) => h.textContent),
    ).toEqual(["Member", "Permission", "Actions"]);
    // Admins first, then members; by name inside each.
    expect(rows.map((r) => within(r).getByRole("rowheader").textContent)).toEqual([
      expect.stringContaining("Ana Souza"),
      expect.stringContaining("Bob Lima"),
      expect.stringContaining("Cora Dias"),
      expect.stringContaining("Dev Reis"),
      expect.stringContaining("Sentinel"),
      expect.stringContaining("Worker 1"),
    ]);
    const bobRow = row(table, "Bob Lima");
    expect(within(bobRow).getByText("@bob")).toBeInTheDocument();
    expect(within(bobRow).getByText("Member")).toBeInTheDocument();
    const workerRow = row(table, "Worker 1");
    expect(within(workerRow).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(workerRow).getByText("@w1")).toBeInTheDocument();
    expect(within(table).queryByText(/Invited by|Guest|Position/)).toBeNull();
  });

  it("marks the viewer's own row", async () => {
    const { table } = await open(asMember());
    expect(within(row(table, "Cora Dias")).getByText("You")).toBeInTheDocument();
    expect(within(row(table, "Bob Lima")).queryByText("You")).toBeNull();
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

describe("who may invite, and as what (OMEM-01, OMEM-04, SR2-01)", () => {
  const dialog = () => screen.findByRole("dialog", { name: "Invite to Acme" });

  it("SR2-01 offers a member no way to invite: only an admin invites", async () => {
    await open(asMember());
    expect(screen.queryByRole("button", { name: "Invite" })).toBeNull();
  });

  it("SR2-01 lets an admin pick admin or member, no guest and no position, and sends the permission", async () => {
    const { calls, user } = await open(
      asAdmin({
        "POST /orgs/o1/invitations": json(
          201,
          makeInvitation({ invitee: eli, permission: "admin" }),
        ),
      }),
    );
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    const permission = within(d).getByRole("combobox", { name: "Permission" });
    expect(
      within(permission)
        .getAllByRole("option")
        .map((o) => o.textContent?.trim()),
    ).toEqual(["Admin", "Member"]);
    expect(permission).toHaveValue("member");
    expect(within(d).queryByRole("combobox", { name: "Position" })).toBeNull();
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "@Eli ");
    await user.selectOptions(permission, "admin");
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({
      handle: "eli",
      permission: "admin",
    });
    expect(await screen.findByText("Invitation sent to Eli Costa")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("asks for a handle before asking the server", async () => {
    const { calls, user } = await open(asAdmin());
    await user.click(screen.getByRole("button", { name: "Invite" }));
    const d = await dialog();
    await user.click(within(d).getByRole("button", { name: "Send invitation" }));
    expect(await within(d).findByText("Required")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("suggests the users and AIs the viewer can see who are not members yet, and fills the handle on a pick", async () => {
    const { user } = await open(asAdmin());
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
    ["an AI member", "sentinel"],
    ["the viewer", "ana"],
  ])("never suggests %s", async (_, typed) => {
    const { user } = await open(asAdmin());
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
        asAdmin({
          "POST /orgs/o1/invitations": apiError(409, "already_member", "already a member"),
        }),
        "bob",
      );
      expect(await within(d).findByText("@bob is already a member of Acme.")).toBeInTheDocument();
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it("says no one has that handle (404 contact_not_found)", async () => {
      const { d } = await submit(
        asAdmin({ "POST /orgs/o1/invitations": apiError(404, "contact_not_found", "not found") }),
        "nobody",
      );
      expect(await within(d).findByText("No one has that handle.")).toBeInTheDocument();
    });

    it("says there were too many invitations (429)", async () => {
      const { d } = await submit(
        asAdmin({ "POST /orgs/o1/invitations": apiError(429, "rate_limited", "slow down") }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Too many invitations in the last hour. Try again later.",
      );
    });

    it("says only admins invite when the server refuses (403): the viewer stopped being one", async () => {
      const { d } = await submit(
        asAdmin({ "POST /orgs/o1/invitations": apiError(403, "forbidden", "forbidden") }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Only admins of this organization invite.",
      );
    });

    it("shows the server's reason for a field (422)", async () => {
      const { d } = await submit(
        asAdmin({
          "POST /orgs/o1/invitations": apiError(422, "invalid", "invalid", {
            permission: "must be admin or member",
          }),
        }),
      );
      expect(await within(d).findByText("must be admin or member")).toBeInTheDocument();
    });

    it("says so when the server cannot be reached", async () => {
      const { d } = await submit(
        asAdmin({
          "POST /orgs/o1/invitations": () => Promise.reject(new TypeError("offline")) as never,
        }),
      );
      expect(await within(d).findByRole("alert")).toHaveTextContent(
        "Can’t reach the server. Check your connection and try again.",
      );
    });
  });

  it("starts empty every time it opens", async () => {
    const { user } = await open(asAdmin());
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
  it("SR2-01 lets an admin change it from the row between admin and member, sending only the permission", async () => {
    const { calls, user, table } = await open(
      asAdmin({ "PATCH /orgs/o1/members/u3": json(200, members[2]) }),
    );
    const select = within(row(table, "Cora Dias")).getByRole("combobox", {
      name: "Permission of Cora Dias",
    });
    expect(select).toHaveValue("member");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent?.trim()),
    ).toEqual(["Admin", "Member"]);
    await user.selectOptions(select, "admin");
    expect(await screen.findByText("Cora Dias is now admin")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ permission: "admin" });
    expect(calls.find((c) => c.method === "PATCH")?.path).toBe("/orgs/o1/members/u3");
  });

  it("offers a member and a host only the permission as text", async () => {
    for (const world of [asMember, asHost]) {
      const { table, unmount } = await open(world());
      expect(within(table).queryByRole("combobox")).toBeNull();
      unmount();
    }
  });

  it("stops offering the admin's controls to an admin who steps down", async () => {
    let org = acmeAs("admin");
    const { user, table } = await open(
      asAdmin({
        "GET /orgs/o1": () => json(200, org),
        "PATCH /orgs/o1/members/u1": () => {
          org = acmeAs("member");
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

  it("says only admins change permissions when the server refuses (403)", async () => {
    const { user, table } = await open(
      asAdmin({ "PATCH /orgs/o1/members/u3": apiError(403, "forbidden", "forbidden") }),
    );
    await user.selectOptions(
      within(row(table, "Cora Dias")).getByRole("combobox", { name: "Permission of Cora Dias" }),
      "admin",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only admins of Acme change permissions.",
    );
  });
});

describe("what the viewer may do about each member (SR2-12, POS-06)", () => {
  it("SR2-12 shows an admin Delete for an AI of the organization and Remove from Acme for anyone else", async () => {
    const { table } = await open(asAdmin());
    expect(labelled(row(table, "Ana Souza"))).toEqual([]);
    expect(labelled(row(table, "Bob Lima"))).toEqual(["Remove from Acme"]);
    expect(labelled(row(table, "Cora Dias"))).toEqual(["Remove from Acme"]);
    expect(labelled(row(table, "Worker 1"))).toEqual(["Delete"]);
    expect(labelled(row(table, "Sentinel"))).toEqual(["Delete"]);
  });

  it("SR2-12 shows the host of an AI Delete on that AI only, and no Move anywhere", async () => {
    const { table } = await open(asHost());
    expect(labelled(row(table, "Worker 1"))).toEqual(["Delete"]);
    for (const who of ["Ana Souza", "Bob Lima", "Cora Dias", "Dev Reis", "Sentinel"]) {
      expect(labelled(row(table, who))).toEqual([]);
    }
    expect(within(table).queryByRole("button", { name: /Move/ })).toBeNull();
  });

  it("shows a member no action at all", async () => {
    const { table } = await open(asMember());
    expect(within(table).queryByRole("button", { name: /Move|Delete|Remove/ })).toBeNull();
  });

  describe("removing someone from the organization (OMEM-06)", () => {
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
          "Cora Dias leaves the groups of Acme. Removing someone only takes them out of Acme: their account and their other organizations stay.",
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

    it("shows home_organization inline: an AI whose home it is is deleted, not removed (409)", async () => {
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

  describe("deleting an AI of the organization (SR2-12)", () => {
    it("SR2-12 lets its host delete it: asks first, then deletes it for good and refreshes the table", async () => {
      let list = hostRows;
      const { calls, user, table } = await open(
        asHost({
          "GET /orgs/o1/members": () => json(200, { items: list }),
          "DELETE /agents/a1": () => {
            list = hostRows.filter((m) => m.intelligence.id !== "a1");
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
});

describe("the invitations still waiting (OMEM-01, OMEM-07)", () => {
  const pending = makeInvitation({
    id: "i1",
    invitee: eli,
    permission: "member",
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
    expect(within(item).getByText("Member")).toBeInTheDocument();
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

  it("is not shown to, nor asked for by, anyone but an admin", async () => {
    const { calls } = await open(
      asHost({ "GET /orgs/o1/invitations": json(200, { items: [pending] }) }),
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
    expect(labelled(row(table, "Cora Dias"))).toEqual(["Remover de Acme"]);
    expect(labelled(row(table, "Worker 1"))).toEqual(["Apagar"]);
    await user.click(screen.getByRole("button", { name: "Convidar" }));
    expect(await screen.findByRole("dialog", { name: "Convidar para Acme" })).toBeInTheDocument();
  });

  it("has no axe violations: the table for each part, the dialog, and in Portuguese", async () => {
    for (const world of [asAdmin, asHost, asMember]) {
      const { container, unmount } = await open(world());
      expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
      unmount();
    }

    const admin = await open(
      asAdmin({
        "GET /orgs/o1/invitations": json(200, {
          items: [
            makeInvitation({ id: "i1", invitee: eli, permission: "member", invited_by: ana }),
          ],
        }),
      }),
    );
    await screen.findByRole("list", { name: "Pending invitations of Acme" });
    expect(await axe(admin.container, AXE_OPTIONS)).toHaveNoViolations();
    await admin.user.click(screen.getByRole("button", { name: "Invite" }));
    expect(
      await axe(await screen.findByRole("dialog", { name: "Invite to Acme" }), AXE_OPTIONS),
    ).toHaveNoViolations();
    admin.unmount();

    useLiveStore.setState({ locale: "pt-BR" });
    api(asAdmin({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) }));
    const pt = renderRoute("/orgs/o1/members");
    await screen.findByRole("table", { name: "Membros de Acme" });
    expect(await axe(pt.container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
