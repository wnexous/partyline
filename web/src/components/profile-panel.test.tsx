import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemberPicker } from "@/components/member-picker";
import { ProfilePanel } from "@/components/profile-panel";
import { RoomFiles } from "@/components/room-files";
import { useProfilePanel, useProfilePanelStore } from "@/hooks/use-profile-panel";
import type { IntelligenceProfile, ProfileMembership, RoomMember } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeContacts,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMembership,
  makeMessage,
  makePerson,
  makeProfile,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderRoute, renderWithRouter } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The profile panel (PRF-01..PRF-06): clicking an identity anywhere opens its profile beside the
 * page, a sidebar on a wide screen and a drawer on a phone. The tests go through the places a
 * user clicks, in the whole app, and through the hook for the rest.
 */

const me = makeMe(); // Ana Souza (@ana), the viewer
const atlas = makeAI(); // Atlas (@atlas), an AI
const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
const lia = makePerson(); // Lia Costa (@lia) on WhatsApp

const BIO = "Keeps the release train on time.";

const profiles: Record<string, IntelligenceProfile> = {
  a1: makeProfile({
    ...atlas,
    bio: BIO,
    ai: {
      owner: me.intelligence,
      folder: "/home/dev/atlas",
      folder_label: "~/atlas",
      app: "claude",
      online: true,
      sessions: [],
    },
    apps: [{ kind: "claude", label: "Claude Code" }],
  }),
  u2: makeProfile({ ...bob, bio: "Runs the infrastructure." }),
  u1: makeProfile({ ...me.intelligence, bio: "Writes the release notes.", can_edit: true }),
  p1: makeProfile({
    ...lia,
    bio: "",
    person: {
      connector: { id: "k1", kind: "whatsapp", name: "ops-line" },
      external_id: "5511999990000",
      name_locked: false,
    },
  }),
};

const profileRoutes = Object.fromEntries(
  Object.entries(profiles).map(([id, profile]) => [`GET /intelligences/${id}`, json(200, profile)]),
);

const members: RoomMember[] = [
  makeMember({ id: "mb-a1", kind: "agent", member_id: "a1", name: "Atlas", intelligence: atlas }),
  makeMember({
    id: "mb-u1",
    kind: "user",
    member_id: "u1",
    name: "Ana Souza",
    intelligence: me.intelligence,
    owner: undefined,
  }),
  makeMember({
    id: "mb-u2",
    kind: "user",
    member_id: "u2",
    name: "Bob Lima",
    intelligence: bob,
    owner: undefined,
  }),
];

const messages = [
  makeMessage({
    seq: 1,
    author_kind: "agent",
    author_id: "a1",
    author_name: "Atlas",
    author: atlas,
    text: "Release is green.",
  }),
  makeMessage({
    seq: 2,
    author_id: "u2",
    author_name: "Bob Lima",
    author: bob,
    text: "Shipping it.",
  }),
  makeMessage({ seq: 3, text: "Thanks both.", author: me.intelligence, author_name: "Ana Souza" }),
];

const dm = makeRoom({
  id: "d1",
  kind: "dm",
  name: "",
  my_level: "post",
  member_count: 2,
  participants: [me.intelligence, atlas],
});

function roomApi(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, { items: [makeConversation({ id: "r1", title: "deploys" })] }),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", my_level: "manage" })),
    "GET /rooms/r1/members": json(200, { items: members }),
    "GET /rooms/r1/messages": json(200, { items: [...messages].reverse(), has_more: false }),
    "GET /rooms/d1": json(200, dm),
    "GET /rooms/d1/members": json(200, { items: [] }),
    "GET /rooms/d1/messages": json(200, { items: [], has_more: false }),
    ...profileRoutes,
    ...extra,
  });
}

/** The panel once the profile of `name` has loaded. */
const panelOf = (name: string) => screen.findByRole("dialog", { name });

/** The value shown next to a label of the panel's details (Status, App), if there is one. */
const detail = (panel: HTMLElement, label: string) =>
  within(panel).queryByText(label, { selector: "dt" })?.nextElementSibling ?? null;

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, paletteOpen: false }));
afterEach(() => vi.unstubAllGlobals());

describe("opening the panel from where an identity shows (PRF-01)", () => {
  it("opens from the name of a timeline author, without leaving the page", async () => {
    roomApi();
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1");
    const row = await screen.findByTestId("message-1");

    await user.click(within(row).getByRole("link", { name: "Atlas" }));
    const panel = await panelOf("Atlas");
    expect(within(panel).getByText("@atlas")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
  });

  it("opens with the keyboard, from a link given focus", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const link = within(await screen.findByTestId("message-1")).getByRole("link", {
      name: "Atlas",
    });
    link.focus();
    await user.keyboard("{Enter}");
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("opens from the avatar of a timeline author", async () => {
    roomApi();
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1");
    const row = await screen.findByTestId("message-2");

    await user.click(within(row).getByRole("link", { name: "Open the profile of Bob Lima" }));
    expect(await panelOf("Bob Lima")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
  });

  it("opens from a member of the member list", async () => {
    roomApi();
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1");
    const member = await screen.findByTestId("member-mb-a1");

    await user.click(within(member).getByRole("link", { name: "Atlas" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
  });

  it("opens from a participant of a DM", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/d1");
    const participant = await screen.findByTestId("participant-a1");

    await user.click(within(participant).getByRole("link", { name: "Atlas" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("opens from the counterpart in the header of a DM", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/d1");
    const header = await screen.findByTestId("conversation-header");

    await user.click(within(header).getByRole("link", { name: "Atlas" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("opens from the face in the header of a DM", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/d1");
    const header = await screen.findByTestId("conversation-header");

    await user.click(within(header).getByRole("link", { name: "Open the profile of Atlas" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("opens from either name in the header of a DM the viewer only reads", async () => {
    // Seen through an AI: the viewer is not one of the two participants, so both are named.
    const between = makeRoom({
      id: "d2",
      kind: "dm",
      name: "",
      my_level: "view",
      participants: [bob, atlas],
    });
    roomApi({
      "GET /rooms/d2": json(200, between),
      "GET /rooms/d2/members": json(200, { items: [] }),
      "GET /rooms/d2/messages": json(200, { items: [], has_more: false }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/d2");
    const header = await screen.findByTestId("conversation-header");

    await user.click(within(header).getByRole("link", { name: "Atlas" }));
    await panelOf("Atlas");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(within(header).getByRole("link", { name: "Bob Lima" }));
    expect(await panelOf("Bob Lima")).toBeInTheDocument();
  });

  it("opens from a person of a bridged chat in the timeline, with no Message", async () => {
    roomApi({
      "GET /rooms/r1/messages": json(200, {
        items: [
          makeMessage({
            seq: 1,
            author_kind: "chat",
            author_id: "c1",
            author_user_id: undefined,
            author_name: "Lia Costa",
            author: lia,
            sender_name: "lia",
            chat: { id: "c1", title: "Front desk", platform: "whatsapp", kind: "group" },
            text: "Hello from the front desk",
          }),
        ],
        has_more: false,
      }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const row = await screen.findByTestId("message-1");

    await user.click(within(row).getByRole("link", { name: "Lia Costa" }));
    const panel = await panelOf("Lia Costa");
    expect(within(panel).queryByRole("button", { name: "Message" })).toBeNull();
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toHaveAttribute(
      "href",
      "/contacts/p1",
    );
  });

  it.each([
    ["an AI of an organization", "a1", "Atlas"],
    ["an AI hosted by the viewer that no organization lists", "a4", "Old helper"],
    ["an AI of an organization hosted by another user", "a5", "Sage"],
    ["a user", "u2", "Bob Lima"],
  ])("opens from %s on the contacts page, which stays where it is", async (_what, id, name) => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      [`GET /intelligences/${id}`]: json(200, makeProfile({ id, display_name: name, handle: id })),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts");
    const row = await screen.findByTestId(`contact-${id}`);

    await user.click(within(row).getByRole("link", { name: new RegExp(name) }));
    expect(await panelOf(name)).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/contacts");
  });

  it("opens from a person a connector has seen", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      "GET /contacts/people": json(200, { items: [lia], has_more: false }),
      "GET /intelligences/p1": json(200, profiles.p1),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    const row = await screen.findByTestId("contact-p1");

    await user.click(within(row).getByRole("link", { name: /Lia Costa/ }));
    expect(await panelOf("Lia Costa")).toBeInTheDocument();
  });

  it("opens from an AI found in the command palette, and the palette closes", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, {
        items: [makeConversation({ id: "r1", title: "deploys" })],
      }),
      "GET /agents": json(200, { items: [makeAgent({ id: "a1", name: "Atlas" })] }),
      "GET /connectors": json(200, { items: [] }),
      ...profileRoutes,
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/");
    await screen.findByRole("list", { name: "Conversations" });

    await user.keyboard("{Control>}k{/Control}");
    const palette = await screen.findByRole("dialog", { name: "Go to" });
    await user.click(await within(palette).findByRole("option", { name: /Atlas/ }));

    expect(await panelOf("Atlas")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull());
    expect(router.state.location.pathname).toBe("/");
  });

  it("opens from the author of a file in the files panel", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /rooms/r1/attachments": json(200, {
        items: [
          {
            id: "f1",
            message_id: "m1",
            message_seq: 1,
            name: "report.pdf",
            mime: "application/pdf",
            size: 2048,
            kind: "document",
            status: "stored",
            author_kind: "agent",
            author_id: "a1",
            author_name: "Atlas",
            author: atlas,
            created_at: "2026-10-19T12:00:00Z",
          },
        ],
        has_more: false,
      }),
      ...profileRoutes,
    });
    const user = userEvent.setup();
    renderWithRouter(
      <>
        <RoomFiles roomId="r1" onOpenMessage={() => {}} />
        <ProfilePanel />
      </>,
    );
    const row = await screen.findByTestId("file-f1");

    await user.click(within(row).getByRole("link", { name: "Atlas" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("opens from a candidate of the member picker, which stays open under it", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /contacts": json(200, makeContacts({ users: [bob] })),
      ...profileRoutes,
    });
    const user = userEvent.setup();
    renderWithRouter(
      <>
        <MemberPicker
          roomId="r1"
          members={[]}
          trigger={<button type="button">Add people</button>}
        />
        <ProfilePanel />
      </>,
    );
    await user.click(await screen.findByRole("button", { name: "Add people" }));
    const picker = await screen.findByRole("dialog", { name: "Add members" });
    const candidate = await within(picker).findByTestId("candidate-user-u2");

    await user.click(within(candidate).getByRole("link", { name: "Bob Lima" }));
    expect(await panelOf("Bob Lima")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Bob Lima" })).toBeNull());
    // Escape closed the panel only: the picker it was opened from is still there.
    expect(screen.getByRole("dialog", { name: "Add members" })).toBeInTheDocument();
  });

  it("opens from any component through the hook, with one call", async () => {
    mockApi({ "GET /me": json(200, me), ...profileRoutes });
    function Somewhere() {
      const profilePanel = useProfilePanel();
      return (
        <button type="button" onClick={() => profilePanel.open("a1")}>
          Who is that?
        </button>
      );
    }
    const user = userEvent.setup();
    renderWithRouter(
      <>
        <Somewhere />
        <ProfilePanel />
      </>,
    );
    await user.click(await screen.findByRole("button", { name: "Who is that?" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("leaves a click with a modifier to the browser, which opens the page", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const link = within(await screen.findByTestId("message-1")).getByRole("link", {
      name: "Atlas",
    });
    expect(link).toHaveAttribute("href", "/contacts/a1");

    // What the page did with each click by the time it reached the document; a click nobody
    // stopped would open the page (a new tab with Ctrl, Cmd or the middle button).
    const prevented: boolean[] = [];
    const record = (event: MouseEvent) => {
      prevented.push(event.defaultPrevented);
      event.preventDefault();
    };
    document.addEventListener("click", record);
    try {
      for (const key of ["Control", "Meta", "Shift"]) {
        await user.keyboard(`{${key}>}`);
        await user.click(link);
        await user.keyboard(`{/${key}}`);
      }
      expect(prevented).toEqual([false, false, false]);
      expect(screen.queryByRole("dialog")).toBeNull();

      // A plain click is the panel's.
      await user.click(link);
      expect(prevented.at(-1)).toBe(true);
      expect(await panelOf("Atlas")).toBeInTheDocument();
    } finally {
      document.removeEventListener("click", record);
    }
  });
});

describe("a sidebar on a wide screen and a drawer on a phone (PRF-02)", () => {
  it("is a sidebar from 768 px up", async () => {
    stubViewport(768);
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await panelOf("Atlas");
    expect(panel).toHaveAttribute("data-layout", "sidebar");
    // Docked to the right edge, not the bottom one.
    expect(panel).toHaveClass("right-0");
    expect(panel).not.toHaveClass("bottom-0");
  });

  it("is a drawer at 360 px", async () => {
    stubViewport(360);
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await panelOf("Atlas");
    expect(panel).toHaveAttribute("data-layout", "drawer");
    // Docked to the bottom edge, not the right one.
    expect(panel).toHaveClass("bottom-0");
    expect(panel).not.toHaveClass("right-0");
    // The same content, whole.
    expect(within(panel).getByText("@atlas")).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Message" })).toBeInTheDocument();
  });

  it("is a drawer just under the breakpoint, at 767 px", async () => {
    stubViewport(767);
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    expect(await panelOf("Atlas")).toHaveAttribute("data-layout", "drawer");
  });
});

describe("what the panel shows (PRF-03)", () => {
  it("asks for the profile again each time it opens, so a bio changed meanwhile shows", async () => {
    let bio = "First bio.";
    const { calls } = roomApi({
      "GET /intelligences/a1": () => json(200, { ...profiles.a1, bio }),
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    const link = within(await screen.findByTestId("message-1")).getByRole("link", {
      name: "Atlas",
    });
    const asked = () => calls.filter((c) => c.path === "/intelligences/a1").length;

    await user.click(link);
    expect(await within(await panelOf("Atlas")).findByText("First bio.")).toBeInTheDocument();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const before = asked();

    bio = "Second bio.";
    await user.click(link);
    expect(await within(await panelOf("Atlas")).findByText("Second bio.")).toBeInTheDocument();
    expect(asked()).toBeGreaterThan(before);
  });

  it("shows an AI's avatar, name, handle, bio, status and app, and no kind (AD-024)", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await panelOf("Atlas");

    expect(within(panel).getByRole("heading", { name: "Atlas" })).toBeInTheDocument();
    expect(within(panel).getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(within(panel).getByText("@atlas")).toBeInTheDocument();
    expect(within(panel).getByText(BIO)).toBeInTheDocument();
    expect(detail(panel, "Kind")).toBeNull();
    expect(within(panel).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(panel).queryByTestId("kind-badge")).toBeNull();
    expect(detail(panel, "Status")).toHaveTextContent("Online");
    expect(detail(panel, "App")).toHaveTextContent("Claude Code");
  });

  it("shows a user like an AI, with no kind, and no status or app of its own", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-2")).getByRole("link", { name: "Bob Lima" }),
    );
    const panel = await panelOf("Bob Lima");

    expect(within(panel).getByText("@bob")).toBeInTheDocument();
    expect(within(panel).getByText("Runs the infrastructure.")).toBeInTheDocument();
    expect(detail(panel, "Kind")).toBeNull();
    expect(within(panel).queryByText(/^(Person|User)$/)).toBeNull();
    expect(detail(panel, "Status")).toBeNull();
    expect(detail(panel, "App")).toBeNull();
    expect(within(panel).queryByText("Online")).toBeNull();
    expect(within(panel).queryByText("Offline")).toBeNull();
  });

  it("follows the live presence of an AI while it is open", async () => {
    roomApi();
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await panelOf("Atlas");
    expect(within(panel).getByText("Online")).toBeInTheDocument();

    act(() => useLiveStore.getState().setPresence("a1", { online: false }));
    await waitFor(() => expect(within(panel).getByText("Offline")).toBeInTheDocument());
    act(() => useLiveStore.getState().setPresence("a1", { online: true }));
    await waitFor(() => expect(within(panel).getByText("Online")).toBeInTheDocument());
  });

  it("names the platform of a person who writes from one, as the app it speaks from", async () => {
    mockApi({ "GET /me": json(200, me), ...profileRoutes });
    function Somewhere() {
      const profilePanel = useProfilePanel();
      return (
        <button type="button" onClick={() => profilePanel.open("p1")}>
          Open Lia
        </button>
      );
    }
    const user = userEvent.setup();
    renderWithRouter(
      <>
        <Somewhere />
        <ProfilePanel />
      </>,
    );
    await user.click(await screen.findByRole("button", { name: "Open Lia" }));
    const panel = await panelOf("Lia Costa");
    expect(detail(panel, "App")).toHaveTextContent("WhatsApp");
    expect(detail(panel, "Kind")).toBeNull();
    expect(within(panel).queryByText("Online")).toBeNull();
  });

  it("shows a skeleton while the profile loads and then the profile", async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    roomApi({
      "GET /intelligences/a1": async () => {
        await gate;
        return json(200, profiles.a1);
      },
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );

    const loading = await screen.findByRole("dialog", { name: "Profile" });
    expect(loading).toHaveAttribute("aria-busy", "true");
    expect(loading.querySelectorAll('[data-slot="skeleton"]').length).toBeGreaterThan(0);
    expect(within(loading).getByRole("status")).toHaveTextContent("Loading…");
    expect(within(loading).queryByRole("button", { name: "Message" })).toBeNull();
    expect(within(loading).getByRole("button", { name: "Close" })).toBeInTheDocument();

    release();
    const panel = await panelOf("Atlas");
    expect(panel).toHaveAttribute("aria-busy", "false");
    expect(panel.querySelector('[data-slot="skeleton"]')).toBeNull();
    // Focus stays on the panel, which was named "Profile" when it opened: the arrival of the
    // profile is said in a live region, or a screen reader would never hear the name.
    expect(within(panel).getByRole("status")).toHaveTextContent("Atlas");
  });

  it("says when the profile could not be loaded, and tries again", async () => {
    let attempts = 0;
    roomApi({
      "GET /intelligences/a1": () => {
        attempts += 1;
        return attempts === 1 ? apiError(500, "internal") : json(200, profiles.a1);
      },
    });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await screen.findByRole("dialog", { name: "Profile" });
    const alert = await within(panel).findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load this profile.");

    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await panelOf("Atlas")).toBeInTheDocument();
  });

  it("says when the viewer cannot see the profile", async () => {
    roomApi({ "GET /intelligences/a1": apiError(404, "not_found") });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await screen.findByRole("dialog", { name: "Profile" });
    expect(await within(panel).findByText("This contact is not on your line")).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: "Message" })).toBeNull();
  });
});

/*
 * The organizations of an identity (OMEM-03) are shown the way the profile page shows them, read-only:
 * a machine is not a concept any more (AD-022) and nothing is shared one by one (AD-016), so neither
 * has a place here, and the ways to delete or remove an identity (POS-05) stay on the page.
 */
describe("the organizations it shares with the viewer (OMEM-03)", () => {
  const acme = makeMembership({
    org_id: "o1",
    org_name: "Acme",
    org_slug: "acme",
    permission: "member",
    position_id: "pos2",
    position_name: "Lead developer",
    delete_effect: "delete",
  });
  const globex = makeMembership({
    org_id: "o2",
    org_name: "Globex",
    org_slug: "globex",
    permission: "guest",
  });
  const atlasIn = (...memberships: ProfileMembership[]) => ({
    "GET /intelligences/a1": json(200, { ...profiles.a1, memberships }),
  });

  async function openAtlas(extra: Parameters<typeof mockApi>[0] = {}) {
    roomApi(extra);
    const user = userEvent.setup();
    const view = renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    return { user, panel: await panelOf("Atlas"), ...view };
  }

  it("lists each one with its permission and position there, and tags the home of an AI", async () => {
    const { panel } = await openAtlas(atlasIn(acme, globex));

    const orgs = within(panel).getByRole("region", { name: "Organizations" });
    expect(within(orgs).getAllByRole("listitem")).toHaveLength(2);
    const inAcme = within(orgs).getByRole("listitem", { name: "Acme" });
    expect(inAcme).toHaveTextContent("Member · Lead developer");
    expect(within(inAcme).getByText("Home organization")).toBeInTheDocument();
    const inGlobex = within(orgs).getByRole("listitem", { name: "Globex" });
    expect(inGlobex).toHaveTextContent("Guest");
    expect(within(inGlobex).queryByText("Home organization")).toBeNull();
  });

  it("has no section for an identity that shares no organization with the viewer", async () => {
    const { panel } = await openAtlas(atlasIn());
    expect(within(panel).queryByRole("region", { name: "Organizations" })).toBeNull();
    expect(within(panel).queryByText("Organizations")).toBeNull();
  });

  it("offers no way to delete or remove it: those stay on the profile page (POS-05, POS-06)", async () => {
    const { panel } = await openAtlas(
      atlasIn({ ...acme, can_delete: true }, { ...globex, can_delete: true }),
    );

    expect(within(panel).getByRole("region", { name: "Organizations" })).toBeInTheDocument();
    expect(within(panel).queryByRole("button", { name: /Delete|Remove/ })).toBeNull();
    expect(within(panel).queryByRole("link", { name: /Delete|Remove/ })).toBeNull();
    // What is one action away is the page, which has them.
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toBeInTheDocument();
  });

  it("shows no machine and offers no sharing (AD-022, AD-016)", async () => {
    const { panel } = await openAtlas(atlasIn(acme));

    // The details are the status and the app: no row for the kind, none for where it runs.
    expect(
      within(panel)
        .getAllByRole("term")
        .map((term) => term.textContent),
    ).toEqual(["Status", "App"]);
    expect(within(panel).queryByText(/machine/i)).toBeNull();
    expect(within(panel).queryByRole("button", { name: /share/i })).toBeNull();
    expect(within(panel).queryByRole("link", { name: /share/i })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    const { panel } = await openAtlas({
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      ...atlasIn({ ...acme, permission: "admin" }),
    });

    const orgs = within(panel).getByRole("region", { name: "Organizações" });
    const inAcme = within(orgs).getByRole("listitem", { name: "Acme" });
    expect(inAcme).toHaveTextContent("Administrador · Lead developer");
    expect(within(inAcme).getByText("Organização principal")).toBeInTheDocument();
  });
});

describe("in Portuguese", () => {
  it("shows the panel, its details and its actions in the app's language", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    // The account's language, or the shell would switch the browser back to it.
    roomApi({ "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const user = userEvent.setup();
    renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    const panel = await panelOf("Atlas");

    expect(detail(panel, "Tipo")).toBeNull();
    expect(within(panel).queryByText(/^IA$/)).toBeNull();
    expect(detail(panel, "Status")).toHaveTextContent("Online");
    expect(detail(panel, "App")).toHaveTextContent("Claude Code");
    expect(within(panel).getByRole("button", { name: "Mensagem" })).toBeInTheDocument();
    expect(within(panel).getByRole("link", { name: "Abrir perfil completo" })).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Fechar" })).toBeInTheDocument();
  });
});

describe("the actions the viewer may use (PRF-04)", () => {
  async function openFrom(seq: number, name: string, route = "/rooms/r1") {
    const user = userEvent.setup();
    const view = renderRoute(route);
    const row = await screen.findByTestId(`message-${seq}`);
    await user.click(within(row).getByRole("link", { name }));
    return { user, ...view };
  }

  it.each([
    ["an AI", 1, "Atlas"],
    ["a user", 2, "Bob Lima"],
  ])("offers Message for %s and opens the DM with it", async (_what, seq, name) => {
    const { calls } = roomApi({
      "POST /conversations/dm": json(200, dm),
    });
    const { user, router } = await openFrom(seq, name);
    const panel = await panelOf(name);

    await user.click(within(panel).getByRole("button", { name: "Message" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST" && c.path === "/conversations/dm")?.body).toEqual({
      with: seq === 1 ? "a1" : "u2",
    });
    await waitFor(() => expect(screen.queryByRole("dialog", { name })).toBeNull());
  });

  it("offers Message for the counterpart of the DM that is already open, and gives focus back", async () => {
    const { calls } = roomApi({ "POST /conversations/dm": json(200, dm) });
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/d1");
    const participant = await screen.findByTestId("participant-a1");
    const link = within(participant).getByRole("link", { name: "Atlas" });
    await user.click(link);
    const panel = await panelOf("Atlas");

    await user.click(within(panel).getByRole("button", { name: "Message" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());
    // The DM is the one the viewer is in: nothing to go to, and focus is not left on the page's body.
    expect(router.state.location.pathname).toBe("/rooms/d1");
    expect(calls.filter((c) => c.method === "POST" && c.path === "/conversations/dm")).toHaveLength(
      1,
    );
    await waitFor(() => expect(link).toHaveFocus());
  });

  it("leaves no members sheet over the DM it goes to, on a phone", async () => {
    stubViewport(360);
    roomApi({ "POST /conversations/dm": json(200, dm) });
    const user = userEvent.setup();
    const { router } = renderRoute("/rooms/r1");
    await user.click(await screen.findByRole("button", { name: "Members (3)" }));
    const sheet = await screen.findByRole("dialog", { name: "Members" });
    await user.click(within(await within(sheet).findByTestId("member-mb-a1")).getByRole("link"));
    const panel = await panelOf("Atlas");

    await user.click(within(panel).getByRole("button", { name: "Message" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    // The conversation is on screen with nothing laid over it.
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await screen.findByTestId("conversation-header")).toBeInTheDocument();
  });

  it("leaves a modified click on Open full profile to the browser and keeps the panel", async () => {
    roomApi();
    const { user, router } = await openFrom(1, "Atlas");
    const panel = await panelOf("Atlas");
    const full = within(panel).getByRole("link", { name: "Open full profile" });

    // As a browser opening a new tab: nobody stops the click, and this tab stays as it was.
    const open = (event: MouseEvent) => event.preventDefault();
    document.addEventListener("click", open);
    try {
      await user.keyboard("{Control>}");
      await user.click(full);
      await user.keyboard("{/Control}");
    } finally {
      document.removeEventListener("click", open);
    }
    expect(screen.getByRole("dialog", { name: "Atlas" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
  });

  it("offers no Message for the viewer's own profile, and Edit profile instead", async () => {
    roomApi();
    const { router, user } = await openFrom(3, "Ana Souza");
    const panel = await panelOf("Ana Souza");

    expect(within(panel).queryByRole("button", { name: "Message" })).toBeNull();
    const edit = within(panel).getByRole("link", { name: "Edit profile" });
    expect(edit).toHaveAttribute("href", "/contacts/u1");
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toHaveAttribute(
      "href",
      "/contacts/u1",
    );

    await user.click(edit);
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/u1"));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Ana Souza" })).toBeNull());
  });

  it("offers no Edit profile on somebody else's profile", async () => {
    roomApi();
    await openFrom(2, "Bob Lima");
    const panel = await panelOf("Bob Lima");
    expect(within(panel).queryByRole("link", { name: "Edit profile" })).toBeNull();
    expect(within(panel).getByRole("button", { name: "Message" })).toBeInTheDocument();
  });

  it("offers no Edit profile for an AI the viewer manages: its page has the editor", async () => {
    roomApi({
      "GET /intelligences/a1": json(200, { ...profiles.a1, can_edit: true }),
    });
    await openFrom(1, "Atlas");
    const panel = await panelOf("Atlas");
    expect(within(panel).getByRole("button", { name: "Message" })).toBeInTheDocument();
    expect(within(panel).queryByRole("link", { name: "Edit profile" })).toBeNull();
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toBeInTheDocument();
  });

  it("offers no Edit profile on one's own profile when it cannot be edited", async () => {
    roomApi({ "GET /intelligences/u1": json(200, { ...profiles.u1, can_edit: false }) });
    await openFrom(3, "Ana Souza");
    const panel = await panelOf("Ana Souza");
    expect(within(panel).queryByRole("link", { name: "Edit profile" })).toBeNull();
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toBeInTheDocument();
  });

  it("offers no Message for a person known through a chat platform", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /contacts": json(200, sampleContacts),
      "GET /contacts/people": json(200, { items: [lia], has_more: false }),
      "GET /intelligences/p1": json(200, profiles.p1),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    await user.click(
      within(await screen.findByTestId("contact-p1")).getByRole("link", { name: /Lia Costa/ }),
    );
    const panel = await panelOf("Lia Costa");

    expect(within(panel).queryByRole("button", { name: "Message" })).toBeNull();
    expect(within(panel).queryByRole("link", { name: "Edit profile" })).toBeNull();
    expect(within(panel).getByRole("link", { name: "Open full profile" })).toHaveAttribute(
      "href",
      "/contacts/p1",
    );
  });

  it("says why when the DM cannot be opened, and keeps the panel", async () => {
    roomApi({ "POST /conversations/dm": apiError(404, "contact_not_found") });
    const { user, router } = await openFrom(1, "Atlas");
    const panel = await panelOf("Atlas");

    await user.click(within(panel).getByRole("button", { name: "Message" }));
    expect(await screen.findByText("Atlas isn’t available anymore.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/rooms/r1");
    expect(screen.getByRole("dialog", { name: "Atlas" })).toBeInTheDocument();
  });

  it("goes to the full profile page, which stays reachable, and closes", async () => {
    roomApi();
    const { user, router } = await openFrom(1, "Atlas");
    const panel = await panelOf("Atlas");

    await user.click(within(panel).getByRole("link", { name: "Open full profile" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/a1"));
    expect(await screen.findByRole("heading", { name: "Atlas", level: 1 })).toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull();
  });
});

describe("closing it and where focus goes (PRF-05)", () => {
  async function openAtlas() {
    roomApi();
    const user = userEvent.setup();
    const view = renderRoute("/rooms/r1");
    const link = within(await screen.findByTestId("message-1")).getByRole("link", {
      name: "Atlas",
    });
    await user.click(link);
    const panel = await panelOf("Atlas");
    return { user, link, panel, ...view };
  }

  it("takes focus inside when it opens and keeps it there while tabbing both ways", async () => {
    const { user, panel } = await openAtlas();
    expect(panel).toContainElement(document.activeElement as HTMLElement);

    for (let i = 0; i < 8; i += 1) {
      await user.tab();
      expect(panel).toContainElement(document.activeElement as HTMLElement);
    }
    for (let i = 0; i < 8; i += 1) {
      await user.tab({ shift: true });
      expect(panel).toContainElement(document.activeElement as HTMLElement);
    }
  });

  it("closes on Escape and gives focus back to the name that opened it", async () => {
    const { user, link } = await openAtlas();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());
    await waitFor(() => expect(link).toHaveFocus());
  });

  it("closes with the close button and gives focus back", async () => {
    const { user, link, panel } = await openAtlas();
    await user.click(within(panel).getByRole("button", { name: "Close" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());
    await waitFor(() => expect(link).toHaveFocus());
  });

  it("closes with a click outside it and gives focus back", async () => {
    const { user, link } = await openAtlas();
    const overlay = document.querySelector('[data-slot="sheet-overlay"]');
    expect(overlay).not.toBeNull();
    await user.click(overlay as Element);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());
    await waitFor(() => expect(link).toHaveFocus());
  });

  it("closes when the page navigates, and takes no focus from the new page", async () => {
    roomApi();
    const { router } = renderRoute("/rooms/r1");
    // Opened from something the next page keeps, so the test can tell whether focus was handed
    // back to it: the sidebar's search box.
    const kept = await screen.findByRole("searchbox", { name: "Search conversations" });
    act(() => useProfilePanelStore.getState().open("a1", kept));
    await panelOf("Atlas");

    await act(() => router.navigate({ to: "/contacts" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());
    // Give Radix the moment it takes before it hands focus back: it must have nothing to hand.
    await new Promise((done) => setTimeout(done, 50));
    expect(kept).not.toHaveFocus();
  });

  it("does not close for a change that leaves the page where it is", async () => {
    roomApi();
    const { router } = renderRoute("/rooms/r1");
    const user = userEvent.setup();
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    await panelOf("Atlas");

    await act(() => router.navigate({ to: "/rooms/$roomId", params: { roomId: "r1" }, hash: "x" }));
    expect(screen.getByRole("dialog", { name: "Atlas" })).toBeInTheDocument();
  });

  it("does not wait to open when the app shell mounts again, as after signing out and in", async () => {
    roomApi();
    const user = userEvent.setup();
    const first = renderRoute("/rooms/r1");
    await user.click(
      within(await screen.findByTestId("message-1")).getByRole("link", { name: "Atlas" }),
    );
    await panelOf("Atlas");

    first.unmount();
    renderRoute("/rooms/r1");
    await screen.findByTestId("message-1");
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens the next identity afresh after it was closed", async () => {
    const { user } = await openAtlas();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Atlas" })).toBeNull());

    await user.click(
      within(await screen.findByTestId("message-2")).getByRole("link", { name: "Bob Lima" }),
    );
    const panel = await panelOf("Bob Lima");
    expect(within(panel).queryByText(BIO)).toBeNull();
    expect(within(panel).getByText("@bob")).toBeInTheDocument();
  });
});
