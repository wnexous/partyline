import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeAIContact,
  makeContacts,
  makeMachineTree,
  makeMe,
  makePerson,
  makeProfile,
  makeRoom,
  makeTreeNode,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";
import { bob, lia, liam, oldHelper, sampleContacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * The contacts page (CONT-01, TREE-01..06): the user's AIs as a tree per machine, the unlinked
 * ones, the AIs shared with them, the people their connectors have seen, and the other users.
 */

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, openRoomId: null }));
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

// Two machines as in the spec's independent test: `a` with `b` inside its folder on one, and on
// the other the home folder with `c/d/e` under it, the folders between them holding no AI.
const A = makeAI({ id: "a1", display_name: "A", handle: "a" });
const B = makeAI({ id: "a2", display_name: "B", handle: "b" });
const HOME = makeAI({ id: "a3", display_name: "Dev", handle: "dev" });
const E = makeAI({ id: "a4", display_name: "E", handle: "e" });

const treeContacts = makeContacts({
  machines: [
    makeMachineTree({
      hostname: "devbox",
      label: "devbox",
      nodes: [
        makeTreeNode({
          name: "a",
          ai: makeAIContact({
            intelligence: A,
            folder: "/home/u/a",
            folder_label: "~/a",
            app: "claude",
            online: true,
          }),
          children: [
            makeTreeNode({
              name: "b",
              ai: makeAIContact({
                intelligence: B,
                folder: "/home/u/a/b",
                folder_label: "~/a/b",
                app: "opencode",
                last_seen_at: "2026-10-19T09:00:00Z",
              }),
            }),
          ],
        }),
      ],
    }),
    makeMachineTree({
      hostname: "notebook-01",
      label: "Laptop",
      nodes: [
        makeTreeNode({
          name: "~",
          ai: makeAIContact({
            intelligence: HOME,
            machine: "notebook-01",
            folder: "/home/ana",
            folder_label: "~",
            app: "gemini",
          }),
          children: [
            makeTreeNode({
              path: "c/d",
              name: "e",
              ai: makeAIContact({
                intelligence: E,
                machine: "notebook-01",
                folder: "/home/ana/c/d/e",
                folder_label: "~/c/d/e",
                app: "codex",
              }),
            }),
          ],
        }),
      ],
    }),
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

const machineHeadings = () =>
  screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent?.trim());
const row = (id: string) => screen.getByTestId(`contact-${id}`);

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

  it("has its sections in order: my AIs, unlinked, shared with me, people, users", async () => {
    api();
    renderRoute("/contacts");
    await screen.findByRole("heading", { name: "My AIs", level: 2 });
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "My AIs",
      "Unlinked",
      "Shared with me",
      "People",
      "Users",
    ]);
  });
});

describe("machines and folders (TREE-01, TREE-02, TREE-03)", () => {
  it("shows each machine as a group, named by the owner's label with the hostname beside a label", async () => {
    api(treeContacts);
    renderRoute("/contacts");
    const devbox = await screen.findByRole("region", { name: "devbox" });
    const laptop = screen.getByRole("region", { name: "Laptop notebook-01" });
    // Two groups, each with its own AIs.
    expect(within(devbox).getByTestId("contact-a1")).toBeInTheDocument();
    expect(within(devbox).queryByTestId("contact-a3")).toBeNull();
    expect(within(laptop).getByTestId("contact-a3")).toBeInTheDocument();
    expect(within(laptop).queryByTestId("contact-a1")).toBeNull();
    // A machine with no label shows its hostname once.
    expect(within(devbox).getAllByText("devbox")).toHaveLength(1);
  });

  it("nests an AI under the AI whose folder holds its own", async () => {
    api(treeContacts);
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    const a = row("a1").closest("li");
    const b = row("a2").closest("li");
    expect(a).toContainElement(row("a2"));
    expect(b).not.toContainElement(row("a1"));
    // The home folder holds the one under `c/d/e` the same way.
    expect(row("a3").closest("li")).toContainElement(row("a4"));
  });

  it("collapses folders that hold no AI into one path before the name, and shows ~ for the home folder", async () => {
    api(treeContacts);
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    expect(within(row("a4")).getByText("c/d ›")).toBeInTheDocument();
    expect(row("a4").textContent).toMatch(/c\/d ›\s*E/);
    expect(within(row("a3")).getByText("~")).toBeInTheDocument();
    expect(within(row("a4")).getByText("~/c/d/e")).toBeInTheDocument();
    // No path before a name when nothing was collapsed.
    expect(within(row("a1")).queryByText(/›/)).toBeNull();
    expect(within(row("a2")).queryByText(/›/)).toBeNull();
  });

  it("shows each AI's name, handle and badge, whether it is online, its app and when it was last seen", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    api(treeContacts);
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });

    const a = within(row("a1"));
    expect(a.getByText("@a")).toBeInTheDocument();
    expect(a.getByText("AI")).toBeInTheDocument();
    expect(a.getByText("Online")).toBeInTheDocument();
    expect(a.getByText("Claude Code")).toBeInTheDocument();
    expect(a.getByText("~/a")).toBeInTheDocument();
    expect(a.queryByText(/Last seen/)).toBeNull();

    const b = within(row("a2"));
    expect(b.getByText("Offline")).toBeInTheDocument();
    expect(b.getByText("OpenCode")).toBeInTheDocument();
    expect(b.getByText("Last seen 3 hr. ago")).toBeInTheDocument();

    expect(within(row("a3")).getByText("Never connected")).toBeInTheDocument();
  });

  it("lists machines with an AI online first, then by name", async () => {
    api(treeContacts);
    const { queryClient } = renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    // devbox has Atlas online; the labels alone would put it first as well.
    expect(machineHeadings()).toEqual(["devbox", "Laptop notebook-01"]);

    // An AI of the other machine comes online: that machine moves to the top.
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: false, last_seen_at: "2026-10-19T11:00:00Z" },
      }),
    );
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a4", online: true },
      }),
    );
    await waitFor(() => expect(machineHeadings()).toEqual(["Laptop notebook-01", "devbox"]));
  });

  it("orders machines by label, ignoring case, when none has an AI online", async () => {
    api(
      makeContacts({
        machines: [
          makeMachineTree({ hostname: "h1", label: "zeta", nodes: [makeTreeNode()] }),
          makeMachineTree({
            hostname: "h2",
            label: "Alpha",
            nodes: [
              makeTreeNode({
                ai: makeAIContact({ intelligence: makeAI({ id: "a9" }), machine: "h2" }),
              }),
            ],
          }),
        ],
      }),
    );
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "Alpha h2" });
    expect(machineHeadings()).toEqual(["Alpha h2", "zeta h1"]);
  });

  it("counts the AIs online in each machine and follows presence", async () => {
    api(treeContacts);
    const { queryClient } = renderRoute("/contacts");
    const devbox = await screen.findByRole("region", { name: "devbox" });
    expect(within(devbox).getByText("1 of 2 online")).toBeInTheDocument();
    expect(
      within(screen.getByRole("region", { name: "Laptop notebook-01" })).getByText("0 of 2 online"),
    ).toBeInTheDocument();

    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a2", online: true },
      }),
    );
    expect(await within(devbox).findByText("2 of 2 online")).toBeInTheDocument();
  });

  it("flips an AI's state and last seen time when a presence event arrives", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    api(treeContacts);
    const { queryClient } = renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
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

describe("naming a machine (TREE-04)", () => {
  /** Opens the rename field of the machine shown as `shown` (its label, or its hostname). */
  const rename = async (
    user: ReturnType<typeof userEvent.setup>,
    shown: string,
    hostname = shown,
  ) => {
    await user.click(await screen.findByRole("button", { name: `Rename machine ${shown}` }));
    return screen.getByRole("textbox", { name: `Name for ${hostname}` });
  };

  it("replaces the hostname with the label, which keeps showing it beside the label", async () => {
    const { calls } = api(treeContacts, {
      "PUT /machines/devbox": json(200, { hostname: "devbox", label: "Build box" }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    // It starts from what is shown now.
    expect(input).toHaveValue("devbox");
    await user.clear(input);
    await user.type(input, "  Build box  {Enter}");

    const group = await screen.findByRole("region", { name: "Build box devbox" });
    expect(within(group).getByText("Build box")).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Name for devbox" })).toBeNull();
    expect(calls.find((c) => c.method === "PUT")).toMatchObject({
      path: "/machines/devbox",
      body: { label: "Build box" },
    });
  });

  it("saves with the save button too, and the new label is what the filter finds", async () => {
    api(treeContacts, {
      "PUT /machines/notebook-01": json(200, { hostname: "notebook-01", label: "Studio" }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "Laptop", "notebook-01");
    expect(input).toHaveValue("Laptop");
    await user.clear(input);
    await user.type(input, "Studio");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByRole("region", { name: "Studio notebook-01" });

    await user.type(screen.getByRole("searchbox", { name: "Filter contacts" }), "studio");
    await waitFor(() => expect(screen.queryByRole("region", { name: "devbox" })).toBeNull());
    expect(screen.getByRole("region", { name: "Studio notebook-01" })).toBeInTheDocument();
  });

  it("brings the hostname back when the label is emptied", async () => {
    const { calls } = api(treeContacts, {
      "PUT /machines/notebook-01": json(200, { hostname: "notebook-01", label: "notebook-01" }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "Laptop", "notebook-01");
    await user.clear(input);
    await user.keyboard("{Enter}");
    await screen.findByRole("region", { name: "notebook-01" });
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ label: "" });
  });

  it("cancels with Escape or the cancel button and sends nothing", async () => {
    const { calls } = api(treeContacts);
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    await user.type(input, "xyz");
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("textbox", { name: "Name for devbox" })).toBeNull();
    expect(screen.getByRole("region", { name: "devbox" })).toBeInTheDocument();

    await rename(user, "devbox");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("textbox", { name: "Name for devbox" })).toBeNull();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("sends nothing when the label did not change", async () => {
    const { calls } = api(treeContacts);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await rename(user, "devbox");
    await user.keyboard("{Enter}");
    expect(screen.queryByRole("textbox", { name: "Name for devbox" })).toBeNull();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("refuses more than 64 characters before asking the server", async () => {
    const { calls } = api(treeContacts);
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    await user.clear(input);
    await user.type(input, "x".repeat(65));
    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByText("Use up to 64 characters.")).toBeInTheDocument();
    await user.keyboard("{Enter}");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    await user.type(input, "{Backspace}");
    expect(input).not.toHaveAttribute("aria-invalid", "true");
  });

  it("accepts exactly 64 characters", async () => {
    const label = "y".repeat(64);
    const { calls } = api(treeContacts, {
      "PUT /machines/devbox": json(200, { hostname: "devbox", label }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    await user.clear(input);
    await user.type(input, `${label}{Enter}`);
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ label }));
  });

  it("shows the server's reason and stays open when it refuses", async () => {
    api(treeContacts, {
      "PUT /machines/devbox": apiError(422, "invalid", "invalid", {
        label: "must have at most 64 characters",
      }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    await user.clear(input);
    await user.type(input, "Build box{Enter}");
    expect(await screen.findByText("must have at most 64 characters")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Name for devbox" })).toBeInTheDocument();
  });

  it("says so when the machine could not be renamed", async () => {
    api(treeContacts, { "PUT /machines/devbox": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts");
    const input = await rename(user, "devbox");
    await user.clear(input);
    await user.type(input, "Build box{Enter}");
    expect(await screen.findByText("Couldn’t rename devbox.")).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Name for devbox" })).toBeInTheDocument();
  });
});

describe("filtering (TREE-05)", () => {
  // No connectors here: a filter also searches the people of each one on the server.
  const noPeople = { ...sampleContacts, people: [] };
  const filter = () => screen.getByRole("searchbox", { name: "Filter contacts" });
  const handles = (region: HTMLElement) =>
    within(region)
      .getAllByText(/^@/)
      .map((e) => e.textContent);

  it("keeps a match with the AIs it hangs from, and nothing else", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "notes");
    const devbox = await screen.findByRole("region", { name: "devbox" });
    // Notes lives in Atlas's folder: Atlas stays, as its parent.
    await waitFor(() => expect(handles(devbox)).toEqual(["@atlas", "@notes"]));
    expect(screen.queryByRole("region", { name: "Laptop notebook-01" })).toBeNull();
  });

  it.each([
    ["a name", "Lab"],
    ["a handle", "@lab"],
    ["a folder", "c/d/lab"],
    ["the collapsed path", "~/c/d"],
    ["an app", "codex"],
  ])("finds an AI by %s", async (_what, text) => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), text);
    const laptop = await screen.findByRole("region", { name: "Laptop notebook-01" });
    await waitFor(() => expect(screen.queryByRole("region", { name: "devbox" })).toBeNull());
    expect(handles(laptop)).toEqual(["@lab"]);
  });

  it("keeps a whole machine when the filter matches its label or its hostname", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "devbox");
    const devbox = await screen.findByRole("region", { name: "devbox" });
    expect(handles(devbox)).toEqual(["@atlas", "@notes"]);
    expect(screen.queryByRole("region", { name: "Laptop notebook-01" })).toBeNull();
  });

  it("matches the label the owner gave a machine and its hostname alike", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "laptop");
    await screen.findByRole("region", { name: "Laptop notebook-01" });
    await waitFor(() => expect(screen.queryByRole("region", { name: "devbox" })).toBeNull());
    await user.clear(filter());
    await user.type(filter(), "notebook-01");
    await screen.findByRole("region", { name: "Laptop notebook-01" });
    expect(screen.queryByRole("region", { name: "devbox" })).toBeNull();
  });

  it("narrows the unlinked, shared and user sections too, and drops a section with no match", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "sage");
    await waitFor(() =>
      expect(screen.queryByRole("heading", { name: "My AIs", level: 2 })).toBeNull(),
    );
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Shared with me",
    ]);
    expect(screen.getByTestId("contact-a5")).toBeInTheDocument();
    expect(screen.queryByTestId("contact-a4")).toBeNull();
    expect(screen.queryByTestId("contact-u2")).toBeNull();

    await user.clear(filter());
    await user.type(filter(), "bob");
    // Bob is a user; Sage is only owned by him, which is not what the filter looks in.
    await waitFor(() => expect(screen.getByTestId("contact-u2")).toBeInTheDocument());
    expect(screen.queryByTestId("contact-a5")).toBeNull();
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual([
      "Users",
    ]);
  });

  it("says when nothing matches, and offers everything back", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "zzzz");
    expect(await screen.findByText("Nothing matches “zzzz”.")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "devbox" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Show all contacts" }));
    expect(await screen.findByRole("region", { name: "devbox" })).toBeInTheDocument();
    expect(filter()).toHaveValue("");
  });

  it("empties the box with its clear button or with Escape", async () => {
    api(noPeople);
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    expect(screen.queryByRole("button", { name: "Clear filter" })).toBeNull();
    await user.type(filter(), "lab");
    await waitFor(() => expect(screen.queryByRole("region", { name: "devbox" })).toBeNull());
    await user.click(screen.getByRole("button", { name: "Clear filter" }));
    expect(await screen.findByRole("region", { name: "devbox" })).toBeInTheDocument();
    expect(filter()).toHaveValue("");

    await user.type(filter(), "lab");
    await waitFor(() => expect(screen.queryByRole("region", { name: "devbox" })).toBeNull());
    await user.keyboard("{Escape}");
    expect(await screen.findByRole("region", { name: "devbox" })).toBeInTheDocument();
    expect(filter()).toHaveValue("");
  });

  it("ignores case and accents", async () => {
    api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    await user.type(filter(), "ATLAS");
    const devbox = await screen.findByRole("region", { name: "devbox" });
    expect(handles(devbox)).toEqual(["@atlas", "@notes"]);
  });
});

describe("unlinked AIs (TREE-06)", () => {
  it("lists the AIs with no folder, with their last seen time and a delete action", async () => {
    api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "Unlinked", level: 2 });
    const section = heading.closest("section") as HTMLElement;
    const old = within(within(section).getByTestId("contact-a4"));
    expect(old.getByText("Old helper")).toBeInTheDocument();
    expect(old.getByText("@old-helper")).toBeInTheDocument();
    expect(old.getByText("Never connected")).toBeInTheDocument();
    expect(old.getByRole("button", { name: "Delete Old helper" })).toBeInTheDocument();
    // They live nowhere: no machine, no folder.
    expect(old.queryByText("devbox")).toBeNull();
  });

  it("deletes one after confirmation, and it leaves the list", async () => {
    let contacts = sampleContacts;
    const { calls } = api(sampleContacts, {
      "GET /contacts": () => json(200, contacts),
      "DELETE /agents/a4": () => {
        contacts = { ...contacts, unlinked: [] };
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

  it("has no unlinked section when every AI has a folder", async () => {
    api(treeContacts);
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    expect(screen.queryByRole("heading", { name: "Unlinked" })).toBeNull();
  });
});

describe("merging an unlinked AI (MRG-01, MRG-02)", () => {
  const mergeButton = () =>
    screen.findByRole("button", { name: "Merge Old helper into another AI" });

  it("picks one of the owner's AIs, merges into it and refreshes the contacts tree", async () => {
    let contacts = sampleContacts;
    const survivor = makeAgent({
      id: "a1",
      intelligence: makeAI({ id: "a1", handle: "atlas", display_name: "Atlas" }),
    });
    const { calls } = api(sampleContacts, {
      "GET /contacts": () => json(200, contacts),
      "POST /agents/a4/merge": () => {
        contacts = { ...sampleContacts, unlinked: [] };
        return json(200, survivor);
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await mergeButton());
    const dialog = await screen.findByRole("dialog", { name: "Merge Old helper into…" });
    const picker = within(dialog).getByRole("combobox", { name: "AI" });
    // The owner's AIs only: never the one being merged away, and never one shared with the user.
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Pick an AI", "Atlas (@atlas)", "Notes (@notes)", "Lab (@lab)"]);
    await user.selectOptions(picker, "a1");
    await user.click(within(dialog).getByRole("button", { name: "Merge AI" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.path).toBe("/agents/a4/merge");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ into: "a1" });
    // The contacts tree is the server's to rebuild: the page asks for it again after the merge.
    await waitFor(() =>
      expect(calls.filter((c) => c.method === "GET" && c.path === "/contacts")).toHaveLength(2),
    );
    await waitFor(() => expect(screen.queryByTestId("contact-a4")).toBeNull());
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("Old helper merged into Atlas")).toBeInTheDocument();
  });

  it("asks to end the session first when the AI is online (MRG-02)", async () => {
    api(sampleContacts, {
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
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(await mergeButton());
    const dialog = await screen.findByRole("dialog", { name: "Merge Old helper into…" });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.some((c) => c.method === "POST")).toBe(false);
    expect(screen.getByTestId("contact-a4")).toBeInTheDocument();
  });

  it("offers it for the owner's unlinked AIs only, never on shared or folder AIs", async () => {
    api();
    renderRoute("/contacts");
    expect(await mergeButton()).toBeInTheDocument();
    for (const id of ["a1", "a5", "u2"]) {
      const row = screen.getByTestId(`contact-${id}`);
      expect(within(row).queryByRole("button", { name: /Merge/ })).toBeNull();
    }
  });

  it("offers no merge when there is no other AI of the owner to merge into", async () => {
    api(
      makeContacts({
        unlinked: [makeAIContact({ intelligence: oldHelper, folder: "", folder_label: "" })],
      }),
    );
    renderRoute("/contacts");
    await screen.findByTestId("contact-a4");
    expect(screen.queryByRole("button", { name: /Merge/ })).toBeNull();
  });
});

describe("shared AIs, users and people (CONT-01)", () => {
  it("lists the AIs shared with the user with whose they are, where they live and whether they are online", async () => {
    api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "Shared with me", level: 2 });
    const sage = within(
      within(heading.closest("section") as HTMLElement).getByTestId("contact-a5"),
    );
    expect(sage.getByText("Sage")).toBeInTheDocument();
    expect(sage.getByText("AI")).toBeInTheDocument();
    expect(sage.getByText("by @bob")).toBeInTheDocument();
    expect(sage.getByText("cloud")).toBeInTheDocument();
    expect(sage.getByText("/srv/sage")).toBeInTheDocument();
    expect(sage.getByText("Offline")).toBeInTheDocument();
  });

  it("lists the other users of the instance", async () => {
    api();
    renderRoute("/contacts");
    const heading = await screen.findByRole("heading", { name: "Users", level: 2 });
    const users = within(heading.closest("section") as HTMLElement);
    expect(users.getByTestId("contact-u2")).toHaveTextContent("Bob Lima");
    expect(users.getByTestId("contact-u2")).toHaveTextContent("@bob");
    expect(users.getByTestId("contact-u3")).toHaveTextContent("Cora Dias");
    // A user is not an AI.
    expect(users.queryByText("AI")).toBeNull();
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
    expect(lia_).toHaveTextContent("WhatsApp");
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
    ["an AI in the tree", "a1", "Atlas"],
    ["an unlinked AI", "a4", "Old helper"],
    ["a shared AI", "a5", "Sage"],
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
    api(treeContacts);
    renderRoute("/contacts");
    await screen.findByRole("region", { name: "devbox" });
    const link = within(row("a1")).getByRole("link");
    expect(link).toHaveAccessibleName("A AI @a");
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
    ["an AI in the tree", "a1", "Atlas"],
    ["an unlinked AI", "a4", "Old helper"],
    ["a shared AI", "a5", "Sage"],
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
    expect(await screen.findByRole("region", { name: "devbox" })).toBeInTheDocument();
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
