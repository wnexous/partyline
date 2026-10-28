import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import type { AIDetail, Contacts, SessionInfo } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeAIContact,
  makeContacts,
  makeMe,
  makeOrgAIs,
  makePerson,
  makeProfile,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";
import { bob, lia, liam, oldHelper, sampleContacts } from "@/test/sample-contacts";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The contacts page (CONT-01, AD-016, AD-022): the AIs of the user's organizations, group by
 * group, the AIs they host that no organization lists, the people their connections have seen and
 * the other users. No machine is shown anywhere.
 */

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, openRoomId: null }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// Two organizations and one without AIs: Acme holds `a`, which is online, and `b` with a last seen
// time; Globex holds the AI of a home folder and `e`; Empty holds none.
const A = makeAI({ id: "a1", display_name: "A", handle: "a" });
const B = makeAI({ id: "a2", display_name: "B", handle: "b" });
const HOME = makeAI({ id: "a3", display_name: "Dev", handle: "dev" });
const E = makeAI({ id: "a4", display_name: "E", handle: "e" });

const orgContacts = makeContacts({
  org_ais: [
    makeOrgAIs({
      org: { id: "o1", name: "Acme", slug: "acme" },
      ais: [
        makeAIContact({
          intelligence: A,
          folder: "/home/u/a",
          folder_label: "~/a",
          app: "claude",
          online: true,
        }),
        makeAIContact({
          intelligence: B,
          folder: "/home/u/a/b",
          folder_label: "~/a/b",
          app: "opencode",
          last_seen_at: "2026-10-19T09:00:00Z",
        }),
      ],
    }),
    makeOrgAIs({
      org: { id: "o2", name: "Globex", slug: "globex" },
      ais: [
        makeAIContact({
          intelligence: HOME,
          org_id: "o2",
          folder: "/home/ana",
          folder_label: "~",
          app: "gemini",
        }),
        makeAIContact({
          intelligence: E,
          org_id: "o2",
          folder: "/home/ana/c/d/e",
          folder_label: "~/c/d/e",
          app: "codex",
        }),
      ],
    }),
    makeOrgAIs({ org: { id: "o3", name: "Empty", slug: "empty" }, ais: [] }),
  ],
});

function api(contacts = sampleContacts, extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [] }),
    "GET /contacts": json(200, contacts),
    ...extra,
  });
}

const row = (id: string) => screen.getByTestId(`contact-${id}`);
const orgRegion = (name: string) => screen.findByRole("region", { name });

describe("the page", () => {
  it("is the contacts page, with a heading and the connect action", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    expect(await screen.findByRole("heading", { name: "Contacts", level: 1 })).toBeInTheDocument();
    await user.click(await screen.findByRole("button", { name: "Connect an agent" }));
    expect(await screen.findByRole("dialog", { name: "Connect an agent" })).toBeInTheDocument();
  });

  it("sends the old agents address here", async () => {
    api();
    const { router } = renderRoute("/agents");
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts"));
    expect(await screen.findByRole("heading", { name: "Contacts", level: 1 })).toBeInTheDocument();
  });

  it("has its sections in order: the organizations, the hosted AIs, people, the rest of the instance", async () => {
    api();
    renderRoute("/contacts");
    await screen.findByRole("heading", { name: "Acme", level: 2 });
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Acme",
      "Globex",
      "Hosted by me",
      "People",
      "On this instance",
    ]);
  });
});

describe("organizations and their AIs (CONT-01, AD-016)", () => {
  it("shows each organization as a group with its own AIs, and leaves one with no AI out", async () => {
    api(orgContacts);
    renderRoute("/contacts");
    const acme = await orgRegion("Acme");
    const globex = screen.getByRole("region", { name: "Globex" });
    expect(within(acme).getByTestId("contact-a1")).toBeInTheDocument();
    expect(within(acme).getByTestId("contact-a2")).toBeInTheDocument();
    expect(within(acme).queryByTestId("contact-a3")).toBeNull();
    expect(within(globex).getByTestId("contact-a3")).toBeInTheDocument();
    expect(within(globex).getByTestId("contact-a4")).toBeInTheDocument();
    expect(within(globex).queryByTestId("contact-a1")).toBeNull();
    expect(screen.queryByRole("region", { name: "Empty" })).toBeNull();
  });

  it("lists an AI that belongs to two organizations under both", async () => {
    api(
      makeContacts({
        org_ais: [
          makeOrgAIs({ ais: [makeAIContact({ intelligence: A })] }),
          makeOrgAIs({
            org: { id: "o2", name: "Globex", slug: "globex" },
            ais: [makeAIContact({ intelligence: A })],
          }),
        ],
      }),
    );
    renderRoute("/contacts");
    const acme = await orgRegion("Acme");
    expect(within(acme).getByText("@a")).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Globex" })).getByText("@a"),
    ).toBeInTheDocument();
  });

  it("shows each AI's name and handle, whether it is online, its app and when it was last seen, with no kind badge", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    api(orgContacts);
    renderRoute("/contacts");
    await orgRegion("Acme");

    const a = within(row("a1"));
    expect(a.getByText("@a")).toBeInTheDocument();
    expect(a.queryByText(/^(AI|IA)$/)).toBeNull();
    expect(a.queryByTestId("kind-badge")).toBeNull();
    expect(a.getByText("Online")).toBeInTheDocument();
    expect(a.getByText("Claude Code")).toBeInTheDocument();
    expect(a.getByText("~/a")).toBeInTheDocument();
    expect(a.queryByText(/Last seen/)).toBeNull();

    const b = within(row("a2"));
    expect(b.getByText("Offline")).toBeInTheDocument();
    expect(b.getByText("OpenCode")).toBeInTheDocument();
    expect(b.getByText("Last seen 3 hr. ago")).toBeInTheDocument();

    expect(within(row("a3")).getByText("Never connected")).toBeInTheDocument();
    expect(within(row("a3")).getByText("~")).toBeInTheDocument();
  });

  it("names the host of an AI somebody else hosts, and shows its folder as that host has it", async () => {
    api();
    renderRoute("/contacts");
    const acme = await orgRegion("Acme");
    const sage = within(within(acme).getByTestId("contact-a5"));
    expect(sage.getByText("Sage")).toBeInTheDocument();
    expect(sage.queryByText(/^(AI|IA)$/)).toBeNull();
    expect(sage.getByText("hosted by @bob")).toBeInTheDocument();
    expect(sage.getByText("/srv/sage")).toBeInTheDocument();
    expect(sage.getByText("Offline")).toBeInTheDocument();
    // Your own AIs name no host.
    expect(within(within(acme).getByTestId("contact-a1")).queryByText(/hosted by/)).toBeNull();
  });

  it("shows no machine anywhere: no hostname, no machine label, no machine group (AD-022)", async () => {
    api(orgContacts);
    renderRoute("/contacts");
    await orgRegion("Acme");
    for (const text of [/devbox/i, /notebook/i, /laptop/i, /machine/i]) {
      expect(screen.queryByText(text)).toBeNull();
    }
    expect(screen.queryByRole("heading", { level: 3 })).toBeNull();
    expect(screen.queryByRole("button", { name: /Rename/ })).toBeNull();
  });

  it("renders no hostname or machine label even when a server still sends them (AD-022)", async () => {
    // What a server of the machine-tree days answered: a hostname and a label on every AI, the
    // machines as a list of their own, the AIs shared with the user apart.
    const stale = {
      ...orgContacts,
      machines: [{ hostname: "devbox", label: "Laptop", folders: [] }],
      shared: [makeAIContact({ intelligence: makeAI({ id: "a9", display_name: "Shared one" }) })],
      org_ais: orgContacts.org_ais.map((group) => ({
        ...group,
        ais: group.ais.map((ai) => ({ ...ai, machine: "devbox", machine_label: "Laptop" })),
      })),
    } as unknown as Contacts;
    api(stale);
    renderRoute("/contacts");
    await orgRegion("Acme");
    expect(screen.queryByText(/devbox|Laptop/)).toBeNull();
    expect(screen.queryByText("Shared one")).toBeNull();
    expect(screen.queryByRole("heading", { name: /shared|machine/i })).toBeNull();
    // Organizations, in the order served, are the only way the AIs are grouped.
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Acme",
      "Globex",
    ]);
  });

  it("counts the AIs online in each organization and follows presence", async () => {
    api(orgContacts);
    const { queryClient } = renderRoute("/contacts");
    const acme = await orgRegion("Acme");
    expect(within(acme).getByText("1 of 2 online")).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Globex" })).getByText("0 of 2 online"),
    ).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a2", online: true },
      }),
    );
    expect(await within(acme).findByText("2 of 2 online")).toBeInTheDocument();
  });

  it("flips an AI's state and last seen time when a presence event arrives", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    api(orgContacts);
    const { queryClient } = renderRoute("/contacts");
    await orgRegion("Acme");
    expect(within(row("a2")).getByText("Last seen 3 hr. ago")).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a2", online: false, last_seen_at: "2026-10-19T11:55:00Z" },
      }),
    );
    expect(within(row("a2")).getByText("Last seen 5 min. ago")).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a2", online: true },
      }),
    );
    expect(within(row("a2")).getByText("Online")).toBeInTheDocument();
    expect(within(row("a2")).queryByText(/Last seen/)).toBeNull();
  });
});

describe("filtering (CONT-01)", () => {
  // No connections here: a filter also searches the people of each one on the server.
  const noPeople = { ...sampleContacts, people: [] };
  const filter = () => screen.getByRole("searchbox", { name: "Filter contacts" });
  const handles = (region: HTMLElement) =>
    within(region)
      .getAllByText(/^@/)
      .map((e) => e.textContent);

  it("keeps the AIs that match and drops the organizations left with none", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "notes");
    const acme = await orgRegion("Acme");
    await waitFor(() => expect(handles(acme)).toEqual(["@notes"]));
    expect(screen.queryByRole("region", { name: "Globex" })).toBeNull();
  });

  it.each([
    ["a name", "Lab"],
    ["a handle", "@lab"],
    ["a folder", "c/d/lab"],
    ["a folder as the home has it", "~/c/d"],
    ["an app", "codex"],
  ])("finds an AI by %s", async (_what, text) => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), text);
    const globex = await orgRegion("Globex");
    await waitFor(() => expect(screen.queryByRole("region", { name: "Acme" })).toBeNull());
    expect(handles(globex)).toEqual(["@lab"]);
  });

  it("keeps a whole organization when the filter matches its name", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "acme");
    const acme = await orgRegion("Acme");
    expect(handles(acme)).toEqual(["@atlas", "@notes", "@sage"]);
    expect(screen.queryByRole("region", { name: "Globex" })).toBeNull();
  });

  it("finds an AI by words that are in its organization's name and in its own text", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "globex lab");
    const globex = await orgRegion("Globex");
    expect(handles(globex)).toEqual(["@lab"]);
    await user.clear(filter());
    await user.type(filter(), "globex notes");
    expect(await screen.findByText("Nothing matches “globex notes”.")).toBeInTheDocument();
  });

  it("narrows the hosted and user sections too, and drops a section with no match", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "sage");
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "Globex", level: 2 })).toBeNull(),
    );
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Acme",
    ]);
    expect(screen.getByTestId("contact-a5")).toBeInTheDocument();
    expect(screen.queryByTestId("contact-a4")).toBeNull();
    expect(screen.queryByTestId("contact-u2")).toBeNull();

    await user.clear(filter());
    await user.type(filter(), "bob");
    // Bob is a user; Sage is only hosted by him, which is not what the filter looks in.
    await waitFor(() => expect(screen.getByTestId("contact-u2")).toBeInTheDocument());
    expect(screen.queryByTestId("contact-a5")).toBeNull();
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "On this instance",
    ]);

    await user.clear(filter());
    await user.type(filter(), "old");
    await waitFor(() => expect(screen.getByTestId("contact-a4")).toBeInTheDocument());
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Hosted by me",
    ]);
  });

  it("says when nothing matches, and offers everything back", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "zzzz");
    expect(await screen.findByText("Nothing matches “zzzz”.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Acme" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Show all contacts" }));
    expect(await orgRegion("Acme")).toBeInTheDocument();
    expect(filter()).toHaveValue("");
  });

  it("empties the box with its clear button or with Escape", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    expect(screen.queryByRole("button", { name: "Clear filter" })).toBeNull();
    await user.type(filter(), "lab");
    await waitFor(() => expect(screen.queryByRole("region", { name: "Acme" })).toBeNull());
    await user.click(screen.getByRole("button", { name: "Clear filter" }));
    expect(await orgRegion("Acme")).toBeInTheDocument();
    expect(filter()).toHaveValue("");

    await user.type(filter(), "lab");
    await waitFor(() => expect(screen.queryByRole("region", { name: "Acme" })).toBeNull());
    await user.keyboard("{Escape}");
    expect(await orgRegion("Acme")).toBeInTheDocument();
    expect(filter()).toHaveValue("");
  });

  it("ignores case and accents", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await orgRegion("Acme");
    await user.type(filter(), "ATLAS");
    const acme = await orgRegion("Acme");
    expect(handles(acme)).toEqual(["@atlas", "@notes"]);
  });
});

describe("hosted AIs", () => {
  it("lists the AIs no organization lists, with their last seen time and a delete action", async () => {
    api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "Hosted by me", level: 2 });
    const section = heading.closest("section") as HTMLElement;
    const old = within(within(section).getByTestId("contact-a4"));
    expect(old.getByText("Old helper")).toBeInTheDocument();
    expect(old.getByText("@old-helper")).toBeInTheDocument();
    expect(old.getByText("Never connected")).toBeInTheDocument();
    expect(old.getByRole("button", { name: "Delete Old helper" })).toBeInTheDocument();
    // They live nowhere the page shows: no organization, no machine.
    expect(old.queryByText("devbox")).toBeNull();
  });

  it("deletes one after confirmation, and it leaves the list", async () => {
    let contacts = sampleContacts;
    const { calls } = api(sampleContacts, {
      "GET /contacts": () => json(200, contacts),
      "DELETE /agents/a4": () => {
        contacts = { ...contacts, hosted: [] };
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: "Delete Old helper" }));
    // Destructive confirmations are alert dialogs, like every other one in the app.
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Old helper?" });
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    await waitFor(() => expect(screen.queryByTestId("contact-a4")).toBeNull());
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual(["/agents/a4"]);
    expect(await screen.findByText("Old helper deleted")).toBeInTheDocument();
  });

  it("keeps it when the confirmation is cancelled", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: "Delete Old helper" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Old helper?" });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(screen.getByTestId("contact-a4")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("says so and keeps the AI when the server refuses", async () => {
    api(sampleContacts, { "DELETE /agents/a4": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: "Delete Old helper" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Old helper?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    expect(await screen.findByText("Couldn’t delete Old helper.")).toBeInTheDocument();
    expect(screen.getByTestId("contact-a4")).toBeInTheDocument();
  });

  it("has no hosted section when every AI belongs to one of the user's organizations", async () => {
    api(orgContacts);
    renderRoute("/contacts");
    await orgRegion("Acme");
    expect(screen.queryByRole("heading", { name: "Hosted by me" })).toBeNull();
  });
});

describe("merging a hosted AI (MRG-01, MRG-02)", () => {
  // The user left Acme: Old helper, Atlas and Notes (home o1) are all hosted, and Lab (home o2).
  const left = makeContacts({
    hosted: [
      makeAIContact({ intelligence: oldHelper, folder: "", folder_label: "" }),
      makeAIContact({ intelligence: makeAI(), online: false }),
      makeAIContact({ intelligence: makeAI({ id: "a2", display_name: "Notes", handle: "notes" }) }),
      makeAIContact({
        intelligence: makeAI({ id: "a3", display_name: "Lab", handle: "lab" }),
        org_id: "o2",
      }),
    ],
  });
  const mergeButton = () =>
    screen.findByRole("button", { name: "Merge Old helper into another AI" });

  it("picks one of the user's AIs of the same organization, merges into it and refreshes the contacts", async () => {
    let contacts = left;
    const survivor = makeAgent({
      id: "a1",
      intelligence: makeAI({ id: "a1", handle: "atlas", display_name: "Atlas" }),
    });
    const { calls } = api(left, {
      "GET /contacts": () => json(200, contacts),
      "POST /agents/a4/merge": () => {
        contacts = makeContacts({ hosted: left.hosted.filter((c) => c.intelligence.id !== "a4") });
        return json(200, survivor);
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await mergeButton());
    const dialog = await screen.findByRole("dialog", { name: "Merge Old helper into…" });
    const picker = within(dialog).getByRole("combobox", { name: "AI" });
    // The user's AIs of the same home organization only: never the one being merged away, never Lab's.
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Pick an AI", "Atlas (@atlas)", "Notes (@notes)"]);
    await user.selectOptions(picker, "a1");
    await user.click(within(dialog).getByRole("button", { name: "Merge AI" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.path).toBe("/agents/a4/merge");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ into: "a1" });
    // The contacts are the server's to rebuild: the page asks for them again after the merge.
    await waitFor(() =>
      expect(calls.filter((c) => c.method === "GET" && c.path === "/contacts")).toHaveLength(2),
    );
    await waitFor(() => expect(screen.queryByTestId("contact-a4")).toBeNull());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("Old helper merged into Atlas")).toBeInTheDocument();
  });

  it("asks to end the session first when the AI is online (MRG-02)", async () => {
    api(left, {
      "POST /agents/a4/merge": apiError(409, "identity_online", "identity is online"),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await mergeButton());
    const dialog = await screen.findByRole("dialog", { name: "Merge Old helper into…" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: "AI" }), "a1");
    await user.click(within(dialog).getByRole("button", { name: "Merge AI" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "End its session first: Old helper is online.",
    );
    // Nothing changed: the dialog stays open, so the merge can be retried once the session ends.
    expect(screen.getByTestId("contact-a4")).toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "Merge Old helper into…" })).toBeInTheDocument();
  });

  it("keeps the AI when the merge is cancelled", async () => {
    const { calls } = api(left);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await mergeButton());
    const dialog = await screen.findByRole("dialog", { name: "Merge Old helper into…" });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(screen.getByTestId("contact-a4")).toBeInTheDocument();
  });

  it("offers it on the hosted AIs only, never on an organization's AIs or a user", async () => {
    api(
      makeContacts({
        org_ais: [
          makeOrgAIs({
            ais: [
              makeAIContact({
                intelligence: makeAI({ id: "a9", display_name: "Sage", handle: "sage" }),
              }),
            ],
          }),
        ],
        hosted: left.hosted,
        users: [bob],
      }),
    );
    renderRoute("/contacts");
    expect(await mergeButton()).toBeInTheDocument();
    for (const id of ["a9", "u2"]) {
      expect(
        within(screen.getByTestId(`contact-${id}`)).queryByRole("button", { name: /Merge/ }),
      ).toBeNull();
    }
  });

  it("offers no merge when the user hosts no other AI of the same organization", async () => {
    api(
      makeContacts({
        hosted: [
          makeAIContact({ intelligence: oldHelper, folder: "", folder_label: "" }),
          makeAIContact({
            intelligence: makeAI({ id: "a3", display_name: "Lab", handle: "lab" }),
            org_id: "o2",
          }),
        ],
      }),
    );
    renderRoute("/contacts");
    await screen.findByTestId("contact-a4");
    expect(screen.queryByRole("button", { name: /Merge/ })).toBeNull();
  });
});

describe("users and people (CONT-01)", () => {
  it("lists the other users of the instance, under a heading that names no kind", async () => {
    api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "On this instance", level: 2 });
    const users = within(heading.closest("section") as HTMLElement);
    expect(users.getByTestId("contact-u2")).toHaveTextContent("Bob Lima");
    expect(users.getByTestId("contact-u2")).toHaveTextContent("@bob");
    expect(users.getByTestId("contact-u3")).toHaveTextContent("Cora Dias");
    expect(screen.queryByRole("heading", { name: "Users" })).toBeNull();
    expect(users.queryByTestId("kind-badge")).toBeNull();
  });

  it("lists the people by connector, with how many each has seen, collapsed until asked", async () => {
    const { calls } = api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "People", level: 2 });
    const people = within(heading.closest("section") as HTMLElement);
    const wa = people.getByRole("button", { name: /ops-line/ });
    expect(wa).toHaveTextContent("WhatsApp");
    expect(wa).toHaveTextContent("2 people");
    expect(wa).toHaveAttribute("aria-expanded", "false");
    expect(people.getByText("quiet-bot")).toBeInTheDocument();
    expect(people.getByText("0 people")).toBeInTheDocument();
    // Nothing is asked until a connector is opened.
    expect(calls.some((c) => c.path === "/contacts/people")).toBe(false);
  });

  it("opens a connector to list its people, asked for that connector only", async () => {
    const { calls } = api(sampleContacts, {
      "GET /contacts/people": json(200, { items: [lia, liam], has_more: false }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const wa = await screen.findByRole("button", { name: /ops-line/ });
    await user.click(wa);
    expect(wa).toHaveAttribute("aria-expanded", "true");
    const lia_ = await screen.findByTestId("contact-p1");
    expect(lia_).toHaveTextContent("Lia Costa");
    expect(lia_).toHaveTextContent("@lia");
    // The platform is the connector's, named once above its people, not a badge on each of them.
    expect(lia_).not.toHaveTextContent("WhatsApp");
    expect(within(lia_).queryByTestId("kind-badge")).toBeNull();
    expect(screen.getByTestId("contact-p2")).toHaveTextContent("Liam Reis");
    const asked = calls.filter((c) => c.path === "/contacts/people");
    expect(asked).toHaveLength(1);
    expect(asked[0]?.url.searchParams.get("connector_id")).toBe("k1");
    expect(asked[0]?.url.searchParams.get("limit")).toBe("50");
    expect(asked[0]?.url.searchParams.has("q")).toBe(false);

    await user.click(wa);
    expect(wa).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("contact-p1")).toBeNull();
  });

  it("does not open a connector that has seen no one", async () => {
    api();
    renderRoute("/contacts");
    await screen.findByRole("heading", { name: "People", level: 2 });
    expect(screen.queryByRole("button", { name: /quiet-bot/ })).toBeNull();
  });

  it("pages through the people with the id of the last one on screen", async () => {
    const first = Array.from({ length: 2 }, (_, i) =>
      makePerson({
        id: `p${10 + i}`,
        display_name: `Person ${10 + i}`,
        handle: `person-${10 + i}`,
      }),
    );
    const second = [makePerson({ id: "p12", display_name: "Person 12", handle: "person-12" })];
    const { calls } = api(sampleContacts, {
      "GET /contacts/people": (url) =>
        url.searchParams.get("after") === "p11"
          ? json(200, { items: second, has_more: false })
          : json(200, { items: first, has_more: true }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    await screen.findByTestId("contact-p10");
    expect(screen.queryByTestId("contact-p12")).toBeNull();

    await user.click(screen.getByRole("button", { name: "Show more" }));
    expect(await screen.findByTestId("contact-p12")).toBeInTheDocument();
    // What was there stays, and there is nothing more to ask for.
    expect(screen.getByTestId("contact-p10")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull();
    const asked = calls.filter((c) => c.path === "/contacts/people");
    expect(asked.map((c) => c.url.searchParams.get("after"))).toEqual([null, "p11"]);
  });

  it("searches the people of each connector on the server once the filter settles", async () => {
    const { calls } = api(sampleContacts, {
      "GET /contacts/people": (url) => {
        const q = (url.searchParams.get("q") ?? "").toLowerCase();
        return json(200, {
          items: [lia, liam].filter((p) => p.display_name.toLowerCase().includes(q)),
          has_more: false,
        });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("heading", { name: "People", level: 2 });
    await user.type(screen.getByRole("searchbox", { name: "Filter contacts" }), "liam");
    // The connector opens by itself to show what was found, and only the settled text was asked.
    expect(await screen.findByTestId("contact-p2")).toHaveTextContent("Liam Reis");
    expect(screen.queryByTestId("contact-p1")).toBeNull();
    const asked = calls.filter((c) => c.path === "/contacts/people");
    expect(asked.length).toBeGreaterThan(0);
    expect(asked.at(-1)?.url.searchParams.get("q")).toBe("liam");
    expect(asked.every((c) => c.url.searchParams.get("connector_id") === "k1")).toBe(true);
  });

  it("says so at a connector whose search finds no one, and has nothing to search at one that saw no one", async () => {
    api(sampleContacts, {
      "GET /contacts/people": json(200, { items: [], has_more: false }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("heading", { name: "People", level: 2 });
    await user.type(screen.getByRole("searchbox", { name: "Filter contacts" }), "zzzz");
    expect(await screen.findByText("No one found.")).toBeInTheDocument();
    // The connector that has seen no one is not searched, so it leaves the list.
    expect(screen.queryByText("quiet-bot")).toBeNull();
    // Something is still being searched, so the page does not say nothing matches.
    expect(screen.queryByText("Nothing matches “zzzz”.")).toBeNull();
  });

  it("says when a connector's people did not load, and tries again on request", async () => {
    let attempts = 0;
    api(sampleContacts, {
      "GET /contacts/people": () => {
        attempts += 1;
        return attempts === 1
          ? apiError(500, "internal", "boom")
          : json(200, { items: [lia], has_more: false });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load the people of ops-line.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await screen.findByTestId("contact-p1")).toBeInTheDocument();
  });
});

describe("opening a profile", () => {
  it.each([
    ["an AI of an organization", "a1", "Atlas"],
    ["a hosted AI", "a4", "Old helper"],
    ["an AI somebody else hosts", "a5", "Sage"],
    ["a user", "u2", "Bob Lima"],
  ])("links the name of %s to its profile", async (_what, id, name) => {
    api(sampleContacts);
    renderRoute("/contacts");
    const link = await within(await screen.findByTestId(`contact-${id}`)).findByRole("link", {
      name: new RegExp(name),
    });
    expect(link).toHaveAttribute("href", `/contacts/${id}`);
  });

  it("names the link by the contact alone, not by the line of details under it", async () => {
    api(orgContacts);
    renderRoute("/contacts");
    await orgRegion("Acme");
    const link = within(row("a1")).getByRole("link");
    expect(link).toHaveAccessibleName("A @a");
  });

  it("links a person to their profile too", async () => {
    api(sampleContacts, { "GET /contacts/people": json(200, { items: [lia], has_more: false }) });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    const link = await within(await screen.findByTestId("contact-p1")).findByRole("link");
    expect(link).toHaveAttribute("href", "/contacts/p1");
  });

  it("opens the profile panel of the contact that was clicked, and the page from the panel (PRF-01, PRF-04)", async () => {
    api(sampleContacts, {
      "GET /intelligences/a1": json(
        200,
        makeProfile({ id: "a1", display_name: "Atlas", handle: "atlas", role: "ai", kind: "ai" }),
      ),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts");
    await user.click(
      await within(await screen.findByTestId("contact-a1")).findByRole("link", { name: /Atlas/ }),
    );
    // A click shows the profile beside the list, which stays where it is...
    const panel = await screen.findByRole("dialog", { name: "Atlas" });
    expect(router.state.location.pathname).toBe("/contacts");

    // ...and the full page is one action away.
    await user.click(within(panel).getByRole("link", { name: "Open full profile" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/a1"));
    expect(await screen.findByRole("heading", { name: "Atlas", level: 1 })).toBeInTheDocument();
  });
});

describe("messaging a contact (DM-01)", () => {
  const dm = makeRoom({ id: "d1", kind: "dm", name: "", my_level: "post", member_count: 2 });

  it.each([
    ["an AI of an organization", "a1", "Atlas"],
    ["a hosted AI", "a4", "Old helper"],
    ["an AI somebody else hosts", "a5", "Sage"],
    ["a user", "u2", "Bob Lima"],
  ])("opens the DM with %s", async (_what, id, name) => {
    const { calls } = api(sampleContacts, {
      "POST /conversations/dm": json(200, dm),
      "GET /rooms/d1": json(200, dm),
      "GET /rooms/d1/members": json(200, { items: [] }),
      "GET /rooms/d1/messages": json(200, { items: [], has_more: false }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: `Message ${name}` }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST")).toMatchObject({
      path: "/conversations/dm",
      body: { with: id },
    });
  });

  it("lists people without a way to message them (CID-22)", async () => {
    api(sampleContacts, {
      "GET /contacts/people": json(200, { items: [lia], has_more: false }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: /ops-line/ }));
    const row = await screen.findByTestId("contact-p1");
    expect(row).toHaveTextContent("Lia Costa");
    // A person is history: they are answered on their platform, not in a DM (DM-05 retired).
    expect(within(row).queryByRole("button", { name: /Message/ })).toBeNull();
  });

  it("says when the contact is gone, and stays on the page", async () => {
    api(sampleContacts, { "POST /conversations/dm": apiError(404, "contact_not_found") });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts");
    await user.click(await screen.findByRole("button", { name: "Message Atlas" }));
    expect(await screen.findByText("Atlas isn’t available anymore.")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/contacts");
  });
});

describe("loading, failing and empty", () => {
  it("shows an error with a way to try again when the contacts do not load", async () => {
    let attempts = 0;
    api(sampleContacts, {
      "GET /contacts": () => {
        attempts += 1;
        return attempts === 1 ? apiError(500, "internal", "boom") : json(200, sampleContacts);
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load your contacts.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await orgRegion("Acme")).toBeInTheDocument();
  });

  it("offers to connect the first AI when there are no contacts at all", async () => {
    api(makeContacts());
    const user = userEvent.setup();
    renderRoute("/contacts");
    expect(await screen.findByRole("heading", { name: "No contacts yet" })).toBeInTheDocument();
    // One connect button: the empty state's own.
    await user.click(screen.getByRole("button", { name: "Connect an agent" }));
    expect(await screen.findByRole("dialog", { name: "Connect an agent" })).toBeInTheDocument();
  });

  it("explains that the user has no AI yet when only other contacts exist", async () => {
    api(makeContacts({ users: [bob] }));
    const user = userEvent.setup();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "My AIs", level: 2 });
    const section = within(heading.closest("section") as HTMLElement);
    expect(section.getByText("No AIs of yours yet.")).toBeInTheDocument();
    expect(screen.getByTestId("contact-u2")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "No contacts yet" })).toBeNull();
    // One button connects an AI, the page header's: not a second one beside the hint.
    expect(section.queryByRole("button", { name: "Connect an agent" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Connect an agent" }));
    expect(await screen.findByRole("dialog", { name: "Connect an agent" })).toBeInTheDocument();
  });
});

describe("the live sessions of an AI's profile (SESS-32)", () => {
  const sessions: SessionInfo[] = [
    { label: "", harness: "claude", connected_at: "2026-10-19T11:00:00Z" },
    { label: "claude-2", harness: "pi", connected_at: "2026-10-19T12:00:00Z" },
  ];
  const atlas = makeAI();
  const ai: AIDetail = {
    owner: makeMe().intelligence,
    folder: "/home/dev/atlas",
    folder_label: "~/atlas",
    app: "claude",
    online: true,
    sessions,
    my_level: "manage",
  };
  const profile = makeProfile({ ...atlas, ai });

  function profileApi(withSessions: SessionInfo[] = sessions) {
    return api(sampleContacts, {
      "GET /intelligences/a1": json(200, { ...profile, ai: { ...ai, sessions: withSessions } }),
    });
  }

  it("lists the principal first, then each labelled session with its harness and start", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T13:00:00Z"));
    profileApi();
    renderRoute("/contacts/a1");
    const section = await screen.findByRole("region", { name: "Live sessions" });
    const rows = within(section).getAllByRole("listitem");
    expect(rows).toHaveLength(2);

    const principal = rows[0] as HTMLElement;
    expect(within(principal).getByTestId("session-label")).toHaveTextContent("principal");
    expect(within(principal).getByText("Claude Code")).toBeInTheDocument();
    expect(
      within(principal)
        .getByText(/^connected /)
        .closest("time"),
    ).toHaveAttribute("datetime", "2026-10-19T11:00:00Z");

    const labelled = rows[1] as HTMLElement;
    expect(within(labelled).getByTestId("session-label")).toHaveTextContent(":claude-2");
    expect(within(labelled).getByText("pi")).toBeInTheDocument();
    expect(
      within(labelled)
        .getByText(/^connected /)
        .closest("time"),
    ).toHaveAttribute("datetime", "2026-10-19T12:00:00Z");
  });

  it("shows no sessions section for an AI that is not live", async () => {
    profileApi([]);
    renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    expect(screen.queryByRole("region", { name: "Live sessions" })).toBeNull();
  });

  it("lists a session labelled principal beside the principal, with distinct keys", async () => {
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    profileApi([
      { label: "", harness: "claude", connected_at: "2026-10-19T11:00:00Z" },
      { label: "principal", harness: "pi", connected_at: "2026-10-19T12:00:00Z" },
    ]);
    renderRoute("/contacts/a1");
    const section = await screen.findByRole("region", { name: "Live sessions" });
    const labels = within(section)
      .getAllByTestId("session-label")
      .map((el) => el.textContent);
    expect(labels).toEqual(["principal", ":principal"]);
    // React reports colliding keys through console.error.
    expect(errors.mock.calls.filter((call) => String(call[0]).includes("same key"))).toEqual([]);
    errors.mockRestore();
  });

  it("renders a profile from a server that sends no sessions or conversations", async () => {
    const { sessions: _s, ...bare } = ai;
    const { conversations: _c, ...rest } = profile;
    api(sampleContacts, {
      "GET /intelligences/a1": json(200, { ...rest, ai: bare }),
    });
    renderRoute("/contacts/a1");
    expect(await screen.findByRole("heading", { name: "Atlas", level: 1 })).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Live sessions" })).toBeNull();
  });

  it("has no axe violations with two live sessions", async () => {
    profileApi();
    const { container } = renderRoute("/contacts/a1");
    await screen.findByRole("region", { name: "Live sessions" });
    expect(
      await axe(container, { rules: { "color-contrast": { enabled: false } } }),
    ).toHaveNoViolations();
  });
});
