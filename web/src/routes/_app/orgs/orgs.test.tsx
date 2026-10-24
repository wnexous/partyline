import { QueryClient } from "@tanstack/react-query";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { CURRENT_ORG_KEY } from "@/lib/orgs";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeInvitation, makeMe, makeOrg } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The organizations page (T40: ORG-01, OMEM-01, OMEM-02, OMEM-07): the viewer's organizations,
 * the way to start one, and the invitations waiting for them or for an AI they manage, with what
 * accepting grants said in words.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };
// Local-time constructors would drift with the zone: the clock is a fixed instant.
const NOW = new Date("2026-10-24T12:00:00Z");

const acme = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "admin" });
const globex = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });
const initech = makeOrg({ id: "o3", name: "Initech", slug: "initech", permission: "guest" });

const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const atlas = makeAI();

function api(over: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /orgs": json(200, { items: [acme, globex, initech] }),
    "GET /invitations": json(200, { items: [] }),
    ...over,
  });
}

const row = (name: string) => screen.findByRole("listitem", { name });
const invitation = (org: string) => screen.findByRole("listitem", { name: `Invitation to ${org}` });

beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null });
  window.localStorage.clear();
  vi.useFakeTimers({ toFake: ["Date"], now: NOW });
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("my organizations (ORG-01, OMEM-03)", () => {
  it("lists every organization of the viewer with its address and what they are in it", async () => {
    api();
    renderRoute("/orgs");
    expect(await screen.findByRole("heading", { level: 1, name: "Organizations" })).toBeVisible();
    const list = await screen.findByRole("list", { name: "Your organizations" });
    const items = within(list).getAllByRole("listitem");
    expect(items.map((i) => i.getAttribute("aria-label"))).toEqual(["Acme", "Globex", "Initech"]);
    expect(within(items[0] as HTMLElement).getByText("acme")).toBeInTheDocument();
    expect(within(items[0] as HTMLElement).getByText("Admin")).toBeInTheDocument();
    expect(within(items[1] as HTMLElement).getByText("Member")).toBeInTheDocument();
    expect(within(items[2] as HTMLElement).getByText("Guest")).toBeInTheDocument();
  });

  it("marks the current organization and switches to another one on request", async () => {
    api({ "GET /me": json(200, { ...makeMe(), default_org_id: "o1" }) });
    const user = userEvent.setup();
    renderRoute("/orgs");
    const acmeRow = await row("Acme");
    expect(within(acmeRow).getByText("Current")).toBeInTheDocument();
    expect(within(acmeRow).queryByRole("button", { name: /^Switch to/ })).toBeNull();

    await user.click(within(await row("Globex")).getByRole("button", { name: "Switch to Globex" }));

    expect(window.localStorage.getItem(CURRENT_ORG_KEY)).toBe("o2");
    expect(within(await row("Globex")).getByText("Current")).toBeInTheDocument();
    expect(within(await row("Acme")).queryByText("Current")).toBeNull();
    expect(within(await row("Acme")).getByRole("button", { name: "Switch to Acme" })).toBeVisible();
  });

  it("asks to create one when the viewer belongs to none", async () => {
    api({ "GET /orgs": json(200, { items: [] }) });
    renderRoute("/orgs");
    expect(
      await screen.findByRole("heading", { name: "You’re not in any organization yet" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Your organizations" })).toBeNull();
    expect(screen.getAllByRole("button", { name: "New organization" }).length).toBeGreaterThan(0);
  });

  it("says so, and offers to try again, when the organizations cannot be read", async () => {
    api({ "GET /orgs": apiError(500, "internal", "boom") });
    renderRoute("/orgs");
    // The sidebar's list has failed to load too: the page's own alert is the one with its text.
    const alert = (await screen.findByText("Couldn’t load your organizations.")).closest(
      "[role=alert]",
    ) as HTMLElement;
    expect(within(alert).getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });

  it("is reachable from the account menu and the switcher", async () => {
    api();
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "Organizations" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
    expect(await screen.findByRole("heading", { level: 1, name: "Organizations" })).toBeVisible();
  });

  it("is linked from the organization switcher as “All organizations”", async () => {
    api({ "GET /conversations": json(200, { items: [] }) });
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await user.click(await screen.findByRole("button", { name: /^Organization: Acme/ }));
    await user.click(await screen.findByRole("menuitem", { name: "All organizations" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
  });
});

describe("a new organization (ORG-01)", () => {
  it("refuses a name of 101 characters inline, before any request", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(await screen.findByRole("button", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.click(within(dialog).getByRole("textbox", { name: "Name" }));
    await user.paste("x".repeat(101));
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));
    expect(await within(dialog).findByText("At most 100 characters")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("creates it with a name of 100 characters, lists it and makes it the current one", async () => {
    const name = "é".repeat(100);
    const created = makeOrg({ id: "o9", name, slug: "e", permission: "admin" });
    let listed = [acme, globex, initech];
    const { calls } = api({
      "GET /orgs": () => json(200, { items: listed }),
      "POST /orgs": () => {
        listed = [acme, created, globex, initech];
        return json(201, created);
      },
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(await screen.findByRole("button", { name: "New organization" }));
    const dialog = await screen.findByRole("dialog", { name: "New organization" });
    await user.click(within(dialog).getByRole("textbox", { name: "Name" }));
    await user.paste(name);
    await user.click(within(dialog).getByRole("button", { name: "Create organization" }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ name });
    const created9 = await row(name);
    expect(within(created9).getByText("Current")).toBeInTheDocument();
    expect(within(created9).getByText("Admin")).toBeInTheDocument();
  });
});

describe("the invitations waiting for the viewer (OMEM-01, OMEM-02)", () => {
  const forMe = makeInvitation({ id: "i1", permission: "member", invited_by: bob });

  it("shows who invited the viewer, to what and with which permission, and when it expires", async () => {
    api({ "GET /invitations": json(200, { items: [forMe] }) });
    renderRoute("/orgs");
    const item = await invitation("Acme");
    expect(within(item).getByText("Bob Lima invited you to Acme as a member.")).toBeInTheDocument();
    expect(within(item).getByText("Expires Nov 3")).toBeInTheDocument();
    expect(within(item).getByRole("button", { name: "Accept" })).toBeInTheDocument();
    expect(within(item).getByRole("button", { name: "Decline" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Invitations" })).toBeInTheDocument();
  });

  it("says how soon one that expires within the week does", async () => {
    api({
      "GET /invitations": json(200, {
        items: [makeInvitation({ id: "i1", expires_at: "2026-10-27T12:00:00Z" })],
      }),
    });
    renderRoute("/orgs");
    expect(within(await invitation("Acme")).getByText("Expires in 3 days")).toBeInTheDocument();
  });

  it("shows no invitations section when none waits", async () => {
    api();
    renderRoute("/orgs");
    await screen.findByRole("list", { name: "Your organizations" });
    expect(screen.queryByRole("heading", { name: "Invitations" })).toBeNull();
    expect(screen.queryByRole("list", { name: "Pending invitations" })).toBeNull();
  });

  it("accepting calls the API and moves the organization into the list", async () => {
    let joined = false;
    const toGlobex = makeInvitation({
      id: "i1",
      org_id: "o2",
      org_name: "Globex",
      org_slug: "globex",
      permission: "member",
      invited_by: bob,
    });
    const { calls } = api({
      "GET /orgs": () => json(200, { items: joined ? [acme, globex] : [acme] }),
      // The server deletes an invitation once it is accepted.
      "GET /invitations": () => json(200, { items: joined ? [] : [toGlobex] }),
      "POST /invitations/i1/accept": () => {
        joined = true;
        return json(200, {});
      },
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    expect(screen.queryByRole("listitem", { name: "Globex" })).toBeNull();
    const item = await invitation("Globex");
    await user.click(within(item).getByRole("button", { name: "Accept" }));

    expect(await row("Globex")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      "/invitations/i1/accept",
    ]);
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Globex" })).toBeNull(),
    );
    expect(await screen.findByText("You joined Globex")).toBeInTheDocument();
    // Nothing waits any more, so the section goes.
    expect(screen.queryByRole("heading", { name: "Invitations" })).toBeNull();
  });

  it("declining calls the API, removes the invitation and adds no organization", async () => {
    const { calls } = api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/decline": new Response(null, { status: 204 }),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(within(await invitation("Acme")).getByRole("button", { name: "Decline" }));
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Acme" })).toBeNull(),
    );
    expect(await screen.findByText("Invitation to Acme declined")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      "/invitations/i1/decline",
    ]);
  });

  it("an expired invitation (410) disappears with a toast", async () => {
    api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/accept": apiError(410, "gone", "invitation expired"),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(within(await invitation("Acme")).getByRole("button", { name: "Accept" }));
    expect(await screen.findByText("The invitation to Acme expired")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Acme" })).toBeNull(),
    );
  });

  it("an invitation the viewer already answered another way (409 already_member) leaves the inbox", async () => {
    api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/accept": apiError(409, "already_member", "already a member"),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(within(await invitation("Acme")).getByRole("button", { name: "Accept" }));
    expect(await screen.findByText("Already a member of Acme")).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Acme" })).toBeNull(),
    );
  });

  it("an invitation that is no longer there (404) leaves the inbox too", async () => {
    api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/decline": apiError(404, "not_found", "gone"),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(within(await invitation("Acme")).getByRole("button", { name: "Decline" }));
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Acme" })).toBeNull(),
    );
  });

  it("keeps the invitation, and says so, when the answer could not be sent", async () => {
    api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/accept": apiError(500, "internal", "boom"),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    await user.click(within(await invitation("Acme")).getByRole("button", { name: "Accept" }));
    expect(
      await screen.findByText("Couldn’t answer the invitation to Acme. Try again."),
    ).toBeInTheDocument();
    expect(await invitation("Acme")).toBeInTheDocument();
    expect(within(await invitation("Acme")).getByRole("button", { name: "Accept" })).toBeEnabled();
  });

  it("sends the answer once while it is on its way", async () => {
    let release: () => void = () => {};
    const { calls } = api({
      "GET /invitations": json(200, { items: [forMe] }),
      "POST /invitations/i1/accept": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(json(200, {}));
        }),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    const item = await invitation("Acme");
    await user.click(within(item).getByRole("button", { name: "Accept" }));
    await waitFor(() =>
      expect(within(item).getByRole("button", { name: "Accept" })).toBeDisabled(),
    );
    expect(within(item).getByRole("button", { name: "Decline" })).toBeDisabled();
    await user.click(within(item).getByRole("button", { name: "Accept" }));
    release();
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });

  it("is refreshed from the server every time the page opens, even within the time a read stays fresh", async () => {
    const { calls } = api({ "GET /invitations": json(200, { items: [forMe] }) });
    // The app keeps a read fresh for 30 s: opening the page again must not rely on it going stale.
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false, staleTime: 30_000 }, mutations: { retry: false } },
    });
    const first = renderRoute("/orgs", client);
    await invitation("Acme");
    const seen = calls.filter((c) => c.path === "/invitations").length;
    first.unmount();
    renderRoute("/orgs", client);
    await invitation("Acme");
    await waitFor(() =>
      expect(calls.filter((c) => c.path === "/invitations").length).toBeGreaterThan(seen),
    );
  });

  it("follows the live refresh of the organizations (an admin withdrew it)", async () => {
    let items = [forMe];
    api({ "GET /invitations": () => json(200, { items }) });
    const { queryClient } = renderRoute("/orgs");
    await invitation("Acme");
    items = [];
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "access.changed",
        data: { resource_kind: "organization", resource_id: "o1" },
      }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("listitem", { name: "Invitation to Acme" })).toBeNull(),
    );
  });
});

/*
 * What accepting grants, in words (L-011: asserted literally). A position puts the invitee under
 * the identities above it; a guest is the responsibility of its inviter, who may remove it.
 */
describe("what the invitation grants (OMEM-01, POS-05)", () => {
  const warned = async (inv: ReturnType<typeof makeInvitation>) => {
    api({ "GET /invitations": json(200, { items: [inv] }) });
    renderRoute("/orgs");
    return invitation(inv.org_name);
  };

  it("says that the identities above the position will manage the invitee", async () => {
    const item = await warned(
      makeInvitation({ permission: "member", position_id: "pos3", position_name: "Worktrees" }),
    );
    expect(within(item).getByText("Position: Worktrees")).toBeInTheDocument();
    expect(
      within(item).getByText("Identities above Worktrees will manage you."),
    ).toBeInTheDocument();
  });

  it("adds that they may remove a guest from the organization", async () => {
    const item = await warned(
      makeInvitation({
        permission: "guest",
        position_id: "pos3",
        position_name: "Worktrees",
        invited_by: bob,
      }),
    );
    expect(within(item).getByText("Bob Lima invited you to Acme as a guest.")).toBeInTheDocument();
    expect(
      within(item).getByText(
        "Identities above Worktrees will manage you and may remove you from Acme.",
      ),
    ).toBeInTheDocument();
  });

  it("names the inviter as the one responsible for a guest with no position", async () => {
    const item = await warned(makeInvitation({ permission: "guest", invited_by: bob }));
    expect(within(item).queryByText(/^Position:/)).toBeNull();
    expect(
      within(item).getByText("Bob Lima will manage you and may remove you from Acme."),
    ).toBeInTheDocument();
  });

  it("grants nothing to say for a member or an admin with no position", async () => {
    const item = await warned(makeInvitation({ permission: "admin", invited_by: bob }));
    expect(within(item).getByText("Bob Lima invited you to Acme as an admin.")).toBeInTheDocument();
    expect(within(item).queryByText(/will manage/)).toBeNull();
  });
});

describe("the invitations of the AIs the viewer manages (OMEM-01)", () => {
  const forAtlas = makeInvitation({
    id: "i7",
    invitee: atlas,
    permission: "guest",
    invited_by: bob,
    org_id: "o2",
    org_name: "Globex",
    org_slug: "globex",
  });

  it("names the AI, accepts for it and says it joined", async () => {
    const { calls } = api({
      "GET /invitations": json(200, { items: [forAtlas] }),
      "POST /invitations/i7/accept": json(200, {}),
    });
    const user = userEvent.setup();
    renderRoute("/orgs");
    const item = await invitation("Globex");
    expect(
      within(item).getByText("Bob Lima invited Atlas to Globex as a guest."),
    ).toBeInTheDocument();
    expect(
      within(item).getByText("Bob Lima will manage Atlas and may remove Atlas from Globex."),
    ).toBeInTheDocument();
    await user.click(within(item).getByRole("button", { name: "Accept" }));
    expect(await screen.findByText("Atlas joined Globex")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "POST").map((c) => c.path)).toEqual([
      "/invitations/i7/accept",
    ]);
  });

  it("tells the invitation of the viewer from the one of their AI when both wait", async () => {
    api({
      "GET /invitations": json(200, {
        items: [forAtlas, makeInvitation({ id: "i1", invited_by: bob })],
      }),
    });
    renderRoute("/orgs");
    expect(await invitation("Acme")).toHaveTextContent("invited you");
    expect(await invitation("Globex")).toHaveTextContent("invited Atlas");
  });
});

describe("in Portuguese and for assistive technology", () => {
  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /invitations": json(200, {
        items: [
          makeInvitation({
            permission: "guest",
            position_id: "pos3",
            position_name: "Worktrees",
            invited_by: bob,
          }),
        ],
      }),
    });
    renderRoute("/orgs");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Organizações" }),
    ).toBeInTheDocument();
    const item = await screen.findByRole("listitem", { name: "Convite para Acme" });
    expect(
      within(item).getByText("Bob Lima convidou você para Acme como convidado."),
    ).toBeInTheDocument();
    expect(
      within(item).getByText(
        "As identidades acima de Worktrees vão gerenciar você e podem remover você de Acme.",
      ),
    ).toBeInTheDocument();
    expect(within(item).getByRole("button", { name: "Aceitar" })).toBeInTheDocument();
    expect(within(item).getByRole("button", { name: "Recusar" })).toBeInTheDocument();
  });

  it("has no axe violations, with organizations and invitations, in both locales", async () => {
    api({
      "GET /invitations": json(200, {
        items: [
          makeInvitation({
            permission: "guest",
            position_id: "pos3",
            position_name: "Worktrees",
            invited_by: bob,
          }),
          makeInvitation({
            id: "i7",
            invitee: atlas,
            org_id: "o2",
            org_name: "Globex",
            invited_by: bob,
          }),
        ],
      }),
    });
    const en = renderRoute("/orgs");
    await invitation("Acme");
    expect(await axe(en.container, AXE_OPTIONS)).toHaveNoViolations();
    en.unmount();

    useLiveStore.setState({ locale: "pt-BR" });
    api({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /invitations": json(200, { items: [makeInvitation({ invited_by: bob })] }),
    });
    const pt = renderRoute("/orgs");
    await screen.findByRole("listitem", { name: "Convite para Acme" });
    expect(await axe(pt.container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("has no axe violations when the viewer belongs to no organization", async () => {
    api({ "GET /orgs": json(200, { items: [] }) });
    const { container } = renderRoute("/orgs");
    await screen.findByRole("heading", { name: "You’re not in any organization yet" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
