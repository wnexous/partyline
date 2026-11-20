import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
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
 * Who can instruct an AI (SR2-07, SR2-10): its host, always first, and the instructors the host named.
 * The host adds one by @handle and removes one; anyone else the server shows the list to only reads it,
 * and a viewer the server does not tell sees no section at all.
 */
describe("SR2-10 who can instruct the AI", () => {
  const hal = makeIntelligence({ id: "u8", display_name: "Hal Prado", handle: "hal" });
  const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
  const lead = makeAI({ id: "a9", display_name: "Lead", handle: "lead" });
  const withInstructors = (
    instructedBy: ReturnType<typeof makeIntelligence>[] | undefined,
    host = hal,
    canEdit = false,
  ) => {
    const p = outdatedProfile();
    if (p.ai) {
      p.ai.sessions = [];
      p.ai.owner = host;
      if (instructedBy) p.ai.instructed_by = instructedBy;
      if (canEdit) p.ai.can_edit_instructors = true;
    }
    p.memberships = [makeMembership({ delete_effect: "delete" })];
    return p;
  };
  const section = () => screen.findByRole("region", { name: "Can instruct this AI" });

  it("SR2-10 lists the host first, then the instructors, and says nothing else makes a message trusted", async () => {
    stubProfile({ "GET /intelligences/a1": json(200, withInstructors([bob, lead])) });
    renderRoute("/contacts/a1");
    const region = await section();
    expect(
      within(region).getByText(
        "Messages from its host and from these people and AIs reach Atlas as trusted: it may act on them as their requests. Nothing else makes a message trusted.",
      ),
    ).toBeVisible();
    const list = within(region).getByRole("list", { name: "Can instruct this AI" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items[0]).toHaveTextContent("Hal Prado");
    expect(items[0]).toHaveTextContent("Host");
    expect(items[1]).toHaveTextContent("Bob Lima");
    expect(items[2]).toHaveTextContent("Lead");
    // Not the host: nothing to change.
    expect(within(region).queryByRole("button")).toBeNull();
  });

  it("SR2-10 says only the host instructs it when the list is empty", async () => {
    stubProfile({ "GET /intelligences/a1": json(200, withInstructors([])) });
    renderRoute("/contacts/a1");
    const region = await section();
    expect(
      within(region).getByText(
        "Only messages from its host reach Atlas as trusted: it treats every other message as information, not as instructions.",
      ),
    ).toBeVisible();
    expect(within(region).getAllByRole("listitem")).toHaveLength(1);
  });

  it("SR2-10 is absent when the server does not tell the viewer", async () => {
    stubProfile({ "GET /intelligences/a1": json(200, withInstructors(undefined)) });
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    expect(screen.queryByRole("region", { name: "Can instruct this AI" })).toBeNull();
  });

  it("SR2-10 lets the host add an instructor by @handle and reads the profile again", async () => {
    const me = makeMe().intelligence;
    let instructors = [bob];
    const { calls } = stubProfile({
      "GET /intelligences/a1": () => json(200, withInstructors(instructors, me, true)),
      "POST /agents/a1/instructors": () => {
        instructors = [bob, lead];
        return json(201, { intelligence: lead, added_at: at });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    const region = await section();
    await user.click(within(region).getByRole("button", { name: "Add instructor" }));
    const d = await screen.findByRole("dialog", { name: "Who else can instruct Atlas?" });
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "@Lead ");
    await user.click(within(d).getByRole("button", { name: "Add" }));
    expect(await screen.findByText("Lead can now instruct Atlas")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ handle: "lead" });
    await waitFor(() => expect(within(region).getAllByRole("listitem")).toHaveLength(3));
  });

  it("SR2-10 says no one has that handle under the field (404 contact_not_found) and keeps the dialog open", async () => {
    const me = makeMe().intelligence;
    stubProfile({
      "GET /intelligences/a1": json(200, withInstructors([], me, true)),
      "POST /agents/a1/instructors": apiError(404, "contact_not_found", "not found"),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    const region = await section();
    await user.click(within(region).getByRole("button", { name: "Add instructor" }));
    const d = await screen.findByRole("dialog", { name: "Who else can instruct Atlas?" });
    await user.type(within(d).getByRole("textbox", { name: "Handle" }), "nobody");
    await user.click(within(d).getByRole("button", { name: "Add" }));
    expect(
      await within(d).findByText("No person or AI you can see has that handle."),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("SR2-10 lets the host remove an instructor after asking", async () => {
    const me = makeMe().intelligence;
    let instructors = [bob, lead];
    const { calls } = stubProfile({
      "GET /intelligences/a1": () => json(200, withInstructors(instructors, me, true)),
      "DELETE /agents/a1/instructors/u2": () => {
        instructors = [lead];
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    const region = await section();
    await user.click(
      within(region).getByRole("button", { name: "Remove Bob Lima as an instructor" }),
    );
    const d = await screen.findByRole("alertdialog", {
      name: "Stop Bob Lima from instructing Atlas?",
    });
    await user.click(within(d).getByRole("button", { name: "Remove" }));
    await waitFor(() =>
      expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
        "/agents/a1/instructors/u2",
      ]),
    );
    await waitFor(() => expect(within(region).queryByText("Bob Lima")).toBeNull());
  });

  it("SR2-10 reads in Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    stubProfile({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "GET /intelligences/a1": json(200, withInstructors([bob])),
    });
    renderRoute("/contacts/a1");
    const region = await screen.findByRole("region", { name: "Pode dar instruções" });
    expect(within(region).getByText("Anfitrião")).toBeVisible();
  });
});
