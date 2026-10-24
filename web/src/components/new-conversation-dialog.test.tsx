import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeContacts, makeConversation, makeMe, makeRoom } from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";
import { atlas, sampleContacts as contacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * "New conversation" (LAY-03, DM-01): pick any intelligence the user can see and open the one DM
 * between them, creating it when there is none.
 */

const dm = (id = "d1") =>
  makeRoom({
    id,
    kind: "dm",
    name: "",
    my_level: "post",
    member_count: 2,
    participants: [makeMe().intelligence, atlas],
  });

function mocks(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [makeConversation({ id: "r1", title: "deploys" })] }),
    "GET /contacts": json(200, contacts),
    "GET /rooms/d1": json(200, dm()),
    "GET /rooms/d1/members": json(200, { items: [] }),
    "GET /rooms/d1/messages": json(200, { items: [], has_more: false }),
    "GET /intelligences/a1": json(200, {
      ...atlas,
      bio: "",
      can_edit: false,
      apps: [],
      conversations: [],
    }),
    ...extra,
  });
}

/** Renders the app, opens "New conversation" from the sidebar and waits for the contacts. */
async function open(extra: Parameters<typeof mockApi>[0] = {}, loaded = true) {
  const api = mocks(extra);
  const user = userEvent.setup();
  const view = renderRoute("/");
  await user.click(await screen.findByRole("button", { name: "New conversation" }));
  const dialog = await screen.findByRole("dialog", { name: "New conversation" });
  if (loaded) await within(dialog).findByTestId("contact-a1");
  return { ...api, ...view, user, dialog };
}
const search = (dialog: HTMLElement) =>
  within(dialog).getByRole("combobox", { name: "Search contacts" });
const shown = (dialog: HTMLElement) =>
  within(dialog)
    .queryAllByRole("option")
    .map((o) => o.getAttribute("data-testid"));

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, paletteOpen: false }));
afterEach(() => vi.unstubAllGlobals());

describe("who can be picked (LAY-03)", () => {
  it("lists the AIs of the user's organizations, the ones they host and the other users in one list by name (AD-024)", async () => {
    const { dialog } = await open();
    expect(shown(dialog)).toEqual([
      "contact-a1",
      "contact-u2",
      "contact-u3",
      "contact-a3",
      "contact-a2",
      "contact-a4",
      "contact-a5",
    ]);
    // One list: no AIs and Users sections.
    expect(within(dialog).queryAllByRole("group")).toEqual([]);
    expect(within(dialog).queryByText(/^(AIs|Users)$/)).toBeNull();
    // The user is not a contact of their own.
    expect(within(dialog).queryByText("Ana Souza")).toBeNull();
  });

  it("tells contacts from each other by handle, state, app and folder, with no kind badge and no machine", async () => {
    const { dialog } = await open();
    const row = (id: string) => within(within(dialog).getByTestId(`contact-${id}`));
    expect(row("a1").getByText("Atlas")).toBeInTheDocument();
    expect(row("a1").getByText("@atlas")).toBeInTheDocument();
    expect(row("a1").getByText("Online")).toBeInTheDocument();
    expect(row("a1").getByText("Claude Code")).toBeInTheDocument();
    expect(row("a1").getByText("~/atlas")).toBeInTheDocument();
    expect(row("a2").getByText("Offline")).toBeInTheDocument();
    expect(row("a2").getByText("OpenCode")).toBeInTheDocument();
    expect(row("a2").getByText("~/atlas/notes")).toBeInTheDocument();
    expect(row("a3").getByText("Codex")).toBeInTheDocument();
    expect(row("a3").getByText("~/c/d/lab")).toBeInTheDocument();
    // An AI another user hosts says who (AD-022: the machine it runs on is not shown).
    expect(row("a5").getByText("hosted by @bob")).toBeInTheDocument();
    expect(within(dialog).queryByText(/devbox|notebook-01|Laptop/)).toBeNull();
    // Nobody, AI or user, carries a badge for what it is.
    expect(within(dialog).queryByTestId("kind-badge")).toBeNull();
    expect(within(dialog).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(row("u2").getByText("@bob")).toBeInTheDocument();
  });

  it("follows live presence for the AIs' online state", async () => {
    const { dialog } = await open();
    const notesRow = within(within(dialog).getByTestId("contact-a2"));
    expect(notesRow.getByText("Offline")).toBeInTheDocument();
    act(() => useLiveStore.getState().setPresence("a2", { online: true }));
    await waitFor(() => expect(notesRow.getByText("Online")).toBeInTheDocument());
  });
});

describe("searching contacts (LAY-03)", () => {
  it.each([
    ["a name", "cora", ["contact-u3"]],
    ["a name, whatever the case", "BOB", ["contact-u2"]],
    ["a handle with @", "@lab", ["contact-a3"]],
    ["an app", "codex", ["contact-a3"]],
    ["a folder", "atlas/notes", ["contact-a2"]],
    ["words that are found in different places", "atlas opencode", ["contact-a2"]],
  ])("finds a contact by %s", async (_what, query, expected) => {
    const { user, dialog } = await open();
    await user.type(search(dialog), query);
    await waitFor(() => expect(shown(dialog)).toEqual(expected));
  });

  it("says when nothing matches, and lists everyone again when the box is cleared", async () => {
    const { user, dialog } = await open();
    await user.type(search(dialog), "zzzz");
    expect(await within(dialog).findByText("Nothing matches “zzzz”.")).toBeInTheDocument();
    expect(shown(dialog)).toEqual([]);
    await user.clear(search(dialog));
    await waitFor(() => expect(shown(dialog)).toHaveLength(7));
  });

  it("starts empty every time it opens", async () => {
    const { user, dialog } = await open();
    await user.type(search(dialog), "bob");
    await waitFor(() => expect(shown(dialog)).toEqual(["contact-u2"]));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "New conversation" }));
    const again = await screen.findByRole("dialog", { name: "New conversation" });
    expect(search(again)).toHaveValue("");
    await within(again).findByTestId("contact-a1");
    expect(shown(again)).toHaveLength(7);
  });
});

describe("opening the conversation (DM-01)", () => {
  it.each([
    [201, "creates the DM when there is none"],
    [200, "opens the DM that already exists"],
  ])("answers %i and %s: the dialog closes and the DM opens", async (status) => {
    const { calls, router, user, dialog } = await open({
      "POST /conversations/dm": json(status, dm()),
    });
    await user.click(within(dialog).getByTestId("contact-a1"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST")).toMatchObject({
      path: "/conversations/dm",
      body: { with: "a1" },
    });
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "New conversation" })).toBeNull(),
    );
  });

  it.each([
    ["a user", "u2"],
    ["an AI of an organization", "a2"],
    ["an AI no organization lists", "a4"],
    ["an AI another user hosts", "a5"],
  ])("opens a DM with %s the same way", async (_what, id) => {
    const { calls, router, user, dialog } = await open({
      "POST /conversations/dm": json(200, dm()),
    });
    await user.click(within(dialog).getByTestId(`contact-${id}`));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ with: id });
  });

  it("opens the first match with Enter", async () => {
    const { calls, router, user, dialog } = await open({
      "POST /conversations/dm": json(200, dm()),
    });
    await user.type(search(dialog), "bob");
    await waitFor(() => expect(shown(dialog)).toEqual(["contact-u2"]));
    await user.keyboard("{Enter}");
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ with: "u2" });
  });

  it("stays open and says so when the contact is gone", async () => {
    const { router, user, dialog } = await open({
      "POST /conversations/dm": apiError(404, "contact_not_found"),
    });
    await user.click(within(dialog).getByTestId("contact-a1"));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Atlas isn’t available anymore.",
    );
    expect(router.state.location.pathname).toBe("/");
    expect(dialog).toBeInTheDocument();
  });

  it("explains a failure that is not about the contact, and a second try can succeed", async () => {
    let attempts = 0;
    const { router, user, dialog } = await open({
      "POST /conversations/dm": () => {
        attempts += 1;
        return attempts === 1 ? apiError(500, "internal", "boom") : json(200, dm());
      },
    });
    await user.click(within(dialog).getByTestId("contact-a1"));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Couldn’t open the conversation. Try again.",
    );
    await user.click(within(dialog).getByTestId("contact-a1"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
  });

  it("clears the message when the dialog is closed and opened again", async () => {
    const { user, dialog } = await open({
      "POST /conversations/dm": apiError(404, "contact_not_found"),
    });
    await user.click(within(dialog).getByTestId("contact-a1"));
    await within(dialog).findByRole("alert");
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "New conversation" }));
    const again = await screen.findByRole("dialog", { name: "New conversation" });
    await within(again).findByTestId("contact-a1");
    expect(within(again).queryByRole("alert")).toBeNull();
  });
});

describe("loading the contacts", () => {
  it("shows an error with a way to try again when they do not load", async () => {
    let attempts = 0;
    const { user, dialog } = await open(
      {
        "GET /contacts": () => {
          attempts += 1;
          return attempts === 1 ? apiError(500, "internal", "boom") : json(200, contacts);
        },
      },
      false,
    );
    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load your contacts.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await within(dialog).findByTestId("contact-a1")).toBeInTheDocument();
  });

  it("says there is no one to message when the user has no contacts at all", async () => {
    const { dialog } = await open({ "GET /contacts": json(200, makeContacts()) }, false);
    expect(await within(dialog).findByText(/No one to message yet/)).toBeInTheDocument();
  });
});
