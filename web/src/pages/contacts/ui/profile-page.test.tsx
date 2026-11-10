import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { json, mockApi } from "@/test/fetch";
import { makeAI, makeIntelligence, makeMe, makeMembership, makeProfile } from "@/test/fixtures";
import { useLiveStore } from "@/test/live-store";
import { renderRoute } from "@/test/render";

/*
 * An AI whose principal session runs an outdated client (SESS-63): the profile header says so once,
 * and the session row warns with the sentence that tells the person to update.
 */

const at = "2026-10-28T14:05:00Z";

beforeEach(() => useLiveStore.setState({ locale: "en", presence: {} }));
afterEach(() => vi.unstubAllGlobals());

function outdatedProfile() {
  return makeProfile({
    ...makeAI(),
    ai: {
      owner: makeIntelligence(),
      folder: "/home/dev/atlas",
      folder_label: "~/atlas",
      app: "claude",
      online: true,
      sessions: [
        { label: "", harness: "claude", connected_at: at, outdated: true },
        { label: "pi-2", harness: "pi", connected_at: at },
      ],
      my_level: "use",
    },
    apps: [{ kind: "claude", label: "Claude Code" }],
  });
}

function stubProfile(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [] }),
    "GET /intelligences/a1": json(200, outdatedProfile()),
    ...extra,
  });
}

describe("an outdated principal session on the profile (SESS-63)", () => {
  it("says it once in the header and warns in the session row", async () => {
    stubProfile();
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });

    expect(await screen.findByTestId("profile-principal-outdated")).toHaveTextContent(
      "The principal session's client is outdated — run partyline update and reopen it",
    );
    expect(screen.getByTestId("session-outdated-principal")).toHaveTextContent(
      "Outdated client — run partyline update and reopen this session",
    );
    expect(screen.queryByTestId("session-outdated-pi-2")).toBeNull();
    expect(screen.getAllByTestId("profile-principal-outdated")).toHaveLength(1);
    expect(screen.queryByTestId("session-notifications-pi-2")).toBeNull();
  });

  it("offers each session's notifications to whoever manages the AI (SN-08)", async () => {
    const managed = outdatedProfile();
    if (managed.ai) managed.ai.my_level = "manage";
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /conversations": json(200, { items: [] }),
      "GET /intelligences/a1": json(200, managed),
      "GET /agents/a1/sessions/pi-2/notifications": json(200, {
        items: [{ room_id: "r1", name: "Support", kind: "group", notify: "mentions", unread: 2 }],
      }),
    });
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });

    await userEvent.setup().click(screen.getByTestId("session-notifications-pi-2"));
    expect(await screen.findByText("Notifications of Atlas :pi-2")).toBeInTheDocument();
    expect(
      await screen.findByRole("switch", { name: "Notifications in Support" }),
    ).not.toBeChecked();
    expect(screen.getByText(/2 unread/)).toBeInTheDocument();
  });

  it("says nothing when every session runs a current client", async () => {
    const current = outdatedProfile();
    if (current.ai) current.ai.sessions = [{ label: "", harness: "claude", connected_at: at }];
    stubProfile({ "GET /intelligences/a1": json(200, current) });
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    expect(screen.queryByTestId("profile-principal-outdated")).toBeNull();
    expect(screen.queryByTestId("session-outdated-principal")).toBeNull();
  });
});

/*
 * Who can instruct an AI (ST-07): the identities whose messages it treats as trusted, as the
 * server lists them for whoever shares its organization; nobody, said in words; and nothing at all
 * for a viewer the server does not tell.
 */
describe("who can instruct the AI (ST-07)", () => {
  const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
  const lead = makeAI({ id: "a9", display_name: "Lead", handle: "lead" });
  const withInstructors = (instructedBy: ReturnType<typeof makeIntelligence>[] | undefined) => {
    const p = outdatedProfile();
    if (p.ai) {
      p.ai.sessions = [];
      if (instructedBy) p.ai.instructed_by = instructedBy;
    }
    p.memberships = [makeMembership({ delete_effect: "delete" })];
    return p;
  };

  it("lists them, says they manage it in its organization and links to Members", async () => {
    stubProfile({
      "GET /intelligences/a1": json(200, withInstructors([makeMe().intelligence, bob, lead])),
    });
    renderRoute("/contacts/a1");
    const section = await screen.findByRole("region", { name: "Who can instruct this AI" });

    expect(
      within(section).getByText(
        "Messages from these people and AIs reach Atlas as trusted: it may act on them as their requests. They are the ones who manage it in Acme.",
      ),
    ).toBeVisible();
    const list = within(section).getByRole("list", { name: "Who can instruct this AI" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(3);
    expect(within(list).getByText("Bob Lima")).toBeVisible();
    expect(within(list).getByText("Lead")).toBeVisible();
    expect(
      within(section).getByRole("link", { name: "Change this on the Members page" }),
    ).toHaveAttribute("href", "/orgs/o1/members");
  });

  it("says that nobody can when the list is empty", async () => {
    stubProfile({ "GET /intelligences/a1": json(200, withInstructors([])) });
    renderRoute("/contacts/a1");
    const section = await screen.findByRole("region", { name: "Who can instruct this AI" });
    expect(
      within(section).getByText(
        "Nobody: Atlas treats every message as information, not as instructions.",
      ),
    ).toBeVisible();
    expect(within(section).queryByRole("list")).toBeNull();
  });

  it("is absent when the server does not tell the viewer", async () => {
    stubProfile({ "GET /intelligences/a1": json(200, withInstructors(undefined)) });
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    expect(screen.queryByRole("region", { name: "Who can instruct this AI" })).toBeNull();
  });

  it("reads in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    stubProfile({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /intelligences/a1": json(200, withInstructors([bob])),
    });
    renderRoute("/contacts/a1");
    expect(
      await screen.findByRole("region", { name: "Quem pode dar ordens a esta IA" }),
    ).toBeVisible();
  });
});
