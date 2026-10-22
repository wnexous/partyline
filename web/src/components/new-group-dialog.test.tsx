import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeContacts,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";
import { sampleContacts as contacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * "New group" (LAY-04): a name and members chosen from the users and AIs the user can add; the
 * server applies the add-member rules to each one and refuses the whole group when any fails.
 */

const group = (id = "g1", name = "release crew") =>
  makeRoom({ id, name, my_level: "manage", member_count: 2 });

function mocks(extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /conversations": json(200, { items: [makeConversation({ id: "r1", title: "deploys" })] }),
    "GET /contacts": json(200, contacts),
    "GET /rooms/g1": json(200, group()),
    "GET /rooms/g1/members": json(200, { items: [] }),
    "GET /rooms/g1/messages": json(200, { items: [], has_more: false }),
    ...extra,
  });
}

/** Renders the app, opens "New group" from the sidebar and waits for the members to load. */
async function open(extra: Parameters<typeof mockApi>[0] = {}, loaded = true) {
  const api = mocks(extra);
  const user = userEvent.setup();
  const view = renderRoute("/");
  await user.click(await screen.findByRole("button", { name: "New group" }));
  const dialog = await screen.findByRole("dialog", { name: "New group" });
  if (loaded) await within(dialog).findByRole("checkbox", { name: "Atlas" });
  return { ...api, ...view, user, dialog };
}
const nameField = (dialog: HTMLElement) => within(dialog).getByRole("textbox", { name: "Name" });
const filter = (dialog: HTMLElement) =>
  within(dialog).getByRole("searchbox", { name: "Filter members" });
const create = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", { name: "Create group" });
const box = (dialog: HTMLElement, name: string) => within(dialog).getByRole("checkbox", { name });
/** The ticked boxes in the order they are listed (the AIs, then the users), not the order they were picked. */
const checked = (dialog: HTMLElement) =>
  within(dialog)
    .getAllByRole("checkbox")
    .filter((c) => c.getAttribute("aria-checked") === "true")
    .map((c) => c.getAttribute("data-member"));
const posted = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/groups");

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, paletteOpen: false }));
afterEach(() => vi.unstubAllGlobals());

describe("who can be a member", () => {
  it("offers the user's AIs and the other users, and no one else", async () => {
    const { dialog } = await open();
    const ais = within(dialog).getByRole("group", { name: "AIs" });
    expect(
      within(ais)
        .getAllByRole("checkbox")
        .map((c) => c.getAttribute("data-member")),
    ).toEqual(["a1", "a2", "a3", "a4", "a5"]);
    const users = within(dialog).getByRole("group", { name: "Users" });
    expect(
      within(users)
        .getAllByRole("checkbox")
        .map((c) => c.getAttribute("data-member")),
    ).toEqual(["u2", "u3"]);
    // People join a group through a bridged chat, never as members.
    expect(within(dialog).queryByRole("group", { name: "People" })).toBeNull();
    expect(within(dialog).queryByText("Lia Costa")).toBeNull();
  });

  it("describes each one the way the conversation picker does: badge, handle, place", async () => {
    const { dialog } = await open();
    const atlas = box(dialog, "Atlas").closest("li") as HTMLElement;
    expect(within(atlas).getByText("AI")).toBeInTheDocument();
    expect(within(atlas).getByText("@atlas")).toBeInTheDocument();
    expect(within(atlas).getByText("devbox")).toBeInTheDocument();
    const sage = box(dialog, "Sage").closest("li") as HTMLElement;
    expect(within(sage).getByText("by @bob")).toBeInTheDocument();
  });

  it("filters by name, handle, machine or folder, and keeps what was picked while hidden", async () => {
    const { user, dialog } = await open();
    await user.click(box(dialog, "Atlas"));
    await user.type(filter(dialog), "bob");
    await waitFor(() =>
      expect(within(dialog).queryByRole("checkbox", { name: "Atlas" })).toBeNull(),
    );
    expect(
      within(dialog)
        .getAllByRole("checkbox")
        .map((c) => c.getAttribute("data-member")),
    ).toEqual(["u2"]);
    // Atlas stays chosen although the filter hides it.
    expect(within(dialog).getByRole("list", { name: "Selected members" })).toHaveTextContent(
      "Atlas",
    );
    await user.clear(filter(dialog));
    await waitFor(() => expect(box(dialog, "Atlas")).toHaveAttribute("aria-checked", "true"));
  });

  it("says when the filter matches no one", async () => {
    const { user, dialog } = await open();
    await user.type(filter(dialog), "zzzz");
    expect(await within(dialog).findByText("Nothing matches that filter.")).toBeInTheDocument();
  });
});

describe("creating the group (LAY-04)", () => {
  it("creates it with the chosen members and opens it", async () => {
    const { calls, router, user, dialog } = await open({
      "POST /groups": json(201, group()),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Atlas"));
    await user.click(box(dialog, "Bob Lima"));
    expect(checked(dialog)).toEqual(["a1", "u2"]);
    await user.click(create(dialog));

    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/g1"));
    expect(posted(calls)).toHaveLength(1);
    expect(posted(calls)[0]?.body).toEqual({ name: "release crew", members: ["a1", "u2"] });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "New group" })).toBeNull());
  });

  it("sends the members in the order they were picked, and the name trimmed", async () => {
    const { calls, user, dialog } = await open({ "POST /groups": json(201, group()) });
    await user.type(nameField(dialog), "  crew  ");
    await user.click(box(dialog, "Cora Dias"));
    await user.click(box(dialog, "Notes"));
    await user.click(box(dialog, "Atlas"));
    await user.click(create(dialog));
    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toEqual({ name: "crew", members: ["u3", "a2", "a1"] });
  });

  it("creates a group with no one in it yet", async () => {
    const { calls, router, user, dialog } = await open({ "POST /groups": json(201, group()) });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/g1"));
    expect(posted(calls)[0]?.body).toEqual({ name: "release crew", members: [] });
  });

  it("asks for a name before sending anything", async () => {
    const { calls, user, dialog } = await open({ "POST /groups": json(201, group()) });
    await user.click(box(dialog, "Atlas"));
    await user.click(create(dialog));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(nameField(dialog)).toHaveAttribute("aria-invalid", "true");
    expect(posted(calls)).toHaveLength(0);
    // A name made of spaces is no name.
    await user.type(nameField(dialog), "   ");
    await user.click(create(dialog));
    expect(await within(dialog).findByText("Required")).toBeInTheDocument();
    expect(posted(calls)).toHaveLength(0);
  });

  it("lists the picked members as a removable summary", async () => {
    const { user, dialog } = await open();
    expect(
      within(dialog).getByText("No one picked yet. You can add members later."),
    ).toBeInTheDocument();
    await user.click(box(dialog, "Atlas"));
    await user.click(box(dialog, "Bob Lima"));
    const picked = within(dialog).getByRole("list", { name: "Selected members" });
    expect(
      within(picked)
        .getAllByRole("listitem")
        .map((li) => li.textContent),
    ).toEqual([expect.stringContaining("Atlas"), expect.stringContaining("Bob Lima")]);
    await user.click(within(picked).getByRole("button", { name: "Remove Atlas" }));
    expect(checked(dialog)).toEqual(["u2"]);
    expect(within(picked).queryByText("Atlas")).toBeNull();
  });

  it("disables the button while the group is being created, so it is made once", async () => {
    let release: () => void = () => {};
    const { calls, user, dialog } = await open({
      "POST /groups": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(json(201, group()));
        }),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    await waitFor(() =>
      expect(within(dialog).getByRole("button", { name: "Creating…" })).toBeDisabled(),
    );
    await user.keyboard("{Enter}");
    expect(posted(calls)).toHaveLength(1);
    release();
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "New group" })).toBeNull());
  });

  it("starts empty each time it opens", async () => {
    const { user, dialog } = await open();
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Atlas"));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await user.click(screen.getByRole("button", { name: "New group" }));
    const again = await screen.findByRole("dialog", { name: "New group" });
    await within(again).findByRole("checkbox", { name: "Atlas" });
    expect(nameField(again)).toHaveValue("");
    expect(checked(again)).toEqual([]);
  });
});

describe("a refused member (LAY-04)", () => {
  const refusal = apiErrorWithRefused([
    { id: "u2", reason: "not_found" },
    { id: "a4", reason: "person" },
  ]);

  it("names every refused member with the reason, creates nothing and stays open", async () => {
    const { calls, router, user, dialog } = await open({ "POST /groups": refusal });
    await user.type(nameField(dialog), "release crew");
    for (const name of ["Atlas", "Bob Lima", "Old helper"]) await user.click(box(dialog, name));
    await user.click(create(dialog));

    const alert = await within(dialog).findByRole("alert");
    expect(alert).toHaveTextContent("These members can’t be added");
    const refused = within(alert).getAllByRole("listitem");
    expect(refused).toHaveLength(2);
    expect(refused[0]).toHaveTextContent("Bob Lima");
    expect(refused[0]).toHaveTextContent(
      "not available: unknown, disabled, or not shared with you",
    );
    expect(refused[1]).toHaveTextContent("Old helper");
    expect(refused[1]).toHaveTextContent("people join through a bridged chat, not as members");
    expect(alert).toHaveTextContent("Nothing was created.");
    expect(router.state.location.pathname).toBe("/");
    expect(dialog).toBeInTheDocument();
    // What the user typed and picked is still there to fix.
    expect(nameField(dialog)).toHaveValue("release crew");
    expect(checked(dialog)).toEqual(["a1", "a4", "u2"]);
    expect(posted(calls)).toHaveLength(1);
  });

  it("flags the refused rows, and creates the group once they are taken out", async () => {
    let attempts = 0;
    const { calls, router, user, dialog } = await open({
      "POST /groups": () => {
        attempts += 1;
        return attempts === 1 ? refusal() : json(201, group());
      },
    });
    await user.type(nameField(dialog), "release crew");
    for (const name of ["Atlas", "Bob Lima", "Old helper"]) await user.click(box(dialog, name));
    await user.click(create(dialog));
    await within(dialog).findByRole("alert");

    expect(box(dialog, "Bob Lima")).toHaveAttribute("aria-invalid", "true");
    expect(box(dialog, "Old helper")).toHaveAttribute("aria-invalid", "true");
    expect(box(dialog, "Atlas")).not.toHaveAttribute("aria-invalid", "true");

    await user.click(box(dialog, "Bob Lima"));
    await user.click(box(dialog, "Old helper"));
    await waitFor(() => expect(within(dialog).queryByRole("alert")).toBeNull());
    await user.click(create(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/g1"));
    expect(posted(calls)[1]?.body).toEqual({ name: "release crew", members: ["a1"] });
  });

  it("falls back to the id for a refused member the contacts do not show", async () => {
    const { user, dialog } = await open({
      "POST /groups": apiErrorWithRefused([{ id: "zz-unknown", reason: "not_found" }]),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    const alert = await within(dialog).findByRole("alert");
    expect(within(alert).getByRole("listitem")).toHaveTextContent("zz-unknown");
  });
});

describe("other failures", () => {
  it("shows the server's reason for the name under the name", async () => {
    const { user, dialog } = await open({
      "POST /groups": apiError(422, "invalid", "invalid", {
        name: "must have 1 to 100 characters",
      }),
    });
    await user.type(nameField(dialog), "x");
    await user.click(create(dialog));
    expect(await within(dialog).findByText("must have 1 to 100 characters")).toBeInTheDocument();
    expect(nameField(dialog)).toHaveAttribute("aria-invalid", "true");
  });

  it("says so, keeping the form, when the group could not be created", async () => {
    const { user, dialog } = await open({ "POST /groups": apiError(500, "internal", "boom") });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Atlas"));
    await user.click(create(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Couldn’t create the group. Try again.",
    );
    expect(nameField(dialog)).toHaveValue("release crew");
    expect(checked(dialog)).toEqual(["a1"]);
  });

  it("reports an unreachable server as such", async () => {
    const { user, dialog } = await open({
      "POST /groups": () => {
        throw new TypeError("failed to fetch");
      },
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Can’t reach the server.");
  });
});

describe("the members list", () => {
  it("shows an error with a way to try again when the contacts do not load", async () => {
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
    expect(await within(dialog).findByRole("checkbox", { name: "Atlas" })).toBeInTheDocument();
  });

  it("says there is no one to add when the user has no AIs or other users", async () => {
    const { dialog } = await open({ "GET /contacts": json(200, makeContacts()) }, false);
    expect(await within(dialog).findByText("No AIs or users to add yet.")).toBeInTheDocument();
  });

  it("stops at the 50 members a new group can start with", async () => {
    const many = makeContacts({
      users: Array.from({ length: 52 }, (_, i) =>
        makeIntelligence({
          id: `u${100 + i}`,
          display_name: `Member ${String(i + 1).padStart(2, "0")}`,
          handle: `member-${i + 1}`,
        }),
      ),
    });
    const { user, dialog } = await open({ "GET /contacts": json(200, many) }, false);
    await within(dialog).findByRole("checkbox", { name: "Member 01" });
    for (let i = 1; i <= 50; i += 1) {
      await user.click(box(dialog, `Member ${String(i).padStart(2, "0")}`));
    }
    expect(checked(dialog)).toHaveLength(50);
    expect(box(dialog, "Member 51")).toBeDisabled();
    expect(within(dialog).getByText("A group starts with at most 50 members.")).toBeInTheDocument();
    // A picked member can still be taken out, which frees a place.
    await user.click(box(dialog, "Member 01"));
    expect(box(dialog, "Member 51")).toBeEnabled();
  }, 60_000);
});

function apiErrorWithRefused(refused: { id: string; reason: string }[]) {
  return () =>
    new Response(
      JSON.stringify({
        error: {
          code: "members_refused",
          message: "some members cannot be added to the group",
          refused,
        },
      }),
      { status: 422, headers: { "Content-Type": "application/json" } },
    );
}
