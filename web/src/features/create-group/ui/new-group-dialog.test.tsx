import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeContacts,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeOrgMember,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { useLiveStore } from "@/test/live-store";
import { memberIn, viewerIn } from "@/test/orgs";
import { renderRoute } from "@/test/render";
import { sampleContacts as contacts } from "@/test/sample-contacts";

vi.mock("@/app/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * "New group" (LAY-04): a name and members chosen from the AIs and users the user can add, listed
 * together by name (AD-024); the server applies the add-member rules to each one and refuses the
 * whole group when any fails.
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
/** The ticked boxes in the order they are listed (by name, AIs and users together), not the order they were picked. */
const checked = (dialog: HTMLElement) =>
  within(dialog)
    .getAllByRole("checkbox")
    .filter((c) => c.getAttribute("aria-checked") === "true")
    .map((c) => c.getAttribute("data-member"));
const posted = (calls: { method: string; path: string; body: unknown }[]) =>
  calls.filter((c) => c.method === "POST" && c.path === "/groups");

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", presence: {}, paletteOpen: false, currentOrgId: null });
  window.localStorage.clear();
});
afterEach(() => vi.unstubAllGlobals());

describe("who can be a member", () => {
  it("offers the AIs of the user's organizations, the ones they host and the other users, and no one else", async () => {
    const { dialog } = await open();
    // One list by name: AIs and users side by side, not an AIs section and a Users section.
    const candidates = within(dialog).getByRole("group", { name: "Contacts" });
    expect(
      within(candidates)
        .getAllByRole("checkbox")
        .map((c) => c.getAttribute("data-member")),
    ).toEqual(["a1", "u2", "u3", "a3", "a2", "a4", "a5"]);
    expect(within(dialog).queryByRole("group", { name: /^(AIs|Users)$/ })).toBeNull();
    // People join a group through a bridged chat, never as members.
    expect(within(dialog).queryByRole("group", { name: "People" })).toBeNull();
    expect(within(dialog).queryByText("Lia Costa")).toBeNull();
  });

  it("describes each one the way the conversation picker does: handle, folder, host, and no kind badge", async () => {
    const { dialog } = await open();
    const atlas = box(dialog, "Atlas").closest("li") as HTMLElement;
    expect(within(atlas).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(dialog).queryByTestId("kind-badge")).toBeNull();
    expect(within(atlas).getByText("@atlas")).toBeInTheDocument();
    expect(within(atlas).getByText("~/atlas")).toBeInTheDocument();
    const sage = box(dialog, "Sage").closest("li") as HTMLElement;
    expect(within(sage).getByText("hosted by @bob")).toBeInTheDocument();
    expect(within(dialog).queryByText(/devbox|notebook-01|Laptop/)).toBeNull();
  });

  it("filters by name, handle, app or folder, and keeps what was picked while hidden", async () => {
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

/*
 * A group belongs to an organization (ORG-02, ORG-03): the dialog asks for it among the ones the
 * viewer can create groups in (a guest cannot, OMEM-04) and offers that organization's AIs. The
 * server refuses a user who is not a member of it, and the dialog says so.
 */
describe("the organization of the group (ORG-03, OMEM-04)", () => {
  // Ana is a member of Acme, an admin of Globex and a guest of Initech.
  const viewer = viewerIn(
    memberIn("o1", "member"),
    memberIn("o2", "admin"),
    memberIn("o3", "guest"),
  );
  const orgField = (dialog: HTMLElement) =>
    within(dialog).findByRole("combobox", { name: "Organization" });
  const optionsOf = (select: HTMLElement) =>
    within(select)
      .getAllByRole("option")
      .map((o) => o.textContent);
  /** Everyone offered, in the order the list shows them. */
  const ids = (dialog: HTMLElement) =>
    within(dialog)
      .getAllByRole("checkbox")
      .map((c) => c.getAttribute("data-member"));

  it("asks for the organization among those the viewer can create groups in, never one they are only a guest of", async () => {
    const { dialog } = await open(viewer);
    expect(optionsOf(await orgField(dialog))).toEqual(["Acme", "Globex"]);
  });

  it("starts on the current organization", async () => {
    useLiveStore.setState({ currentOrgId: "o2" });
    const { dialog } = await open(viewer, false);
    expect(await orgField(dialog)).toHaveValue("o2");
    expect(await within(dialog).findByRole("checkbox", { name: "Lab" })).toBeInTheDocument();
  });

  it("starts on the first organization they can create in when the current one is a guest one", async () => {
    useLiveStore.setState({ currentOrgId: "o3" });
    const { dialog } = await open(viewer);
    expect(await orgField(dialog)).toHaveValue("o1");
  });

  it("offers the AIs that are members of the organization and the users of the instance, and no hosted AI of none", async () => {
    const { dialog } = await open(viewer);
    expect(ids(dialog)).toEqual(["a1", "u2", "u3", "a2", "a5"]);
    expect(within(dialog).queryByRole("checkbox", { name: "Lab" })).toBeNull();
    expect(within(dialog).queryByRole("checkbox", { name: "Old helper" })).toBeNull();
  });

  it("says that only members of the organization can join", async () => {
    const { dialog } = await open(viewer);
    expect(
      await within(dialog).findByText("Only members of the organization can join the group."),
    ).toBeInTheDocument();
  });

  describe("the users offered are the organization's members (T39)", () => {
    // Bob belongs to Acme and Cora to Globex; Ana, the viewer, to both. Dev is only in the directory.
    const bobM = makeOrgMember({
      intelligence: makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" }),
    });
    const coraM = makeOrgMember({
      intelligence: makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" }),
    });
    const anaM = makeOrgMember({ intelligence: makeIntelligence(), permission: "admin" });
    const members = {
      "GET /orgs/o1/members": json(200, { items: [anaM, bobM] }),
      "GET /orgs/o2/members": json(200, { items: [anaM, coraM] }),
    };

    it("offers the users who are members of the organization instead of every user of the instance", async () => {
      const { dialog } = await open({ ...viewer, ...members });
      expect(ids(dialog)).toEqual(["a1", "u2", "a2", "a5"]);
      expect(within(dialog).queryByRole("checkbox", { name: "Cora Dias" })).toBeNull();
    });

    it("does not offer the viewer to themselves, nor the AIs among the members: those come from the contacts", async () => {
      const aiM = makeOrgMember({
        intelligence: makeIntelligence({
          id: "a9",
          kind: "ai",
          role: "ai",
          display_name: "Ghost",
          handle: "ghost",
        }),
      });
      const { dialog } = await open({
        ...viewer,
        "GET /orgs/o1/members": json(200, { items: [anaM, bobM, aiM] }),
      });
      expect(ids(dialog)).toEqual(["a1", "u2", "a2", "a5"]);
      expect(within(dialog).queryByRole("checkbox", { name: "Ghost" })).toBeNull();
    });

    it("lists the members by name", async () => {
      const zed = makeOrgMember({
        intelligence: makeIntelligence({ id: "u7", display_name: "Zed Alves", handle: "zed" }),
      });
      const { dialog } = await open({
        ...viewer,
        "GET /orgs/o1/members": json(200, { items: [zed, anaM, bobM] }),
      });
      expect(ids(dialog)).toEqual(["a1", "u2", "a2", "a5", "u7"]);
    });

    it("offers the members of the other organization when it changes, and drops a pick who is not one of them", async () => {
      const { calls, user, dialog } = await open({
        ...viewer,
        ...members,
        "POST /groups": json(201, group()),
      });
      await user.click(box(dialog, "Bob Lima"));
      expect(checked(dialog)).toEqual(["u2"]);

      await user.selectOptions(await orgField(dialog), "o2");

      expect(
        await within(dialog).findByRole("checkbox", { name: "Cora Dias" }),
      ).toBeInTheDocument();
      expect(ids(dialog)).toEqual(["u3", "a3"]);
      // Bob, picked for Acme, is not a member of Globex.
      await waitFor(() => expect(checked(dialog)).toEqual([]));
      expect(within(dialog).queryByRole("list", { name: "Selected members" })).toBeNull();

      await user.type(nameField(dialog), "release crew");
      await user.click(box(dialog, "Cora Dias"));
      await user.click(create(dialog));
      await waitFor(() => expect(posted(calls)).toHaveLength(1));
      expect(posted(calls)[0]?.body).toEqual({
        name: "release crew",
        members: ["u3"],
        org_id: "o2",
      });
    });

    it("waits for the members, so the whole instance is not listed first", async () => {
      let release: () => void = () => {};
      const { dialog } = await open(
        {
          ...viewer,
          "GET /orgs/o1/members": () =>
            new Promise<Response>((resolve) => {
              release = () => resolve(json(200, { items: [anaM, bobM] }));
            }),
        },
        false,
      );
      await within(dialog).findByRole("textbox", { name: "Name" });
      expect(within(dialog).queryByRole("checkbox")).toBeNull();
      release();
      expect(await within(dialog).findByRole("checkbox", { name: "Bob Lima" })).toBeInTheDocument();
      expect(within(dialog).queryByRole("checkbox", { name: "Cora Dias" })).toBeNull();
    });

    it("offers every user of the instance when the members cannot be read, and the server still refuses non-members", async () => {
      const { dialog } = await open({
        ...viewer,
        "GET /orgs/o1/members": apiError(500, "internal", "boom"),
      });
      expect(ids(dialog)).toEqual(["a1", "u2", "u3", "a2", "a5"]);
    });
  });

  it("creates the group in the organization, and sends it with the members", async () => {
    const { calls, router, user, dialog } = await open({
      ...viewer,
      "POST /groups": json(201, group()),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Atlas"));
    await user.click(box(dialog, "Bob Lima"));
    await user.click(create(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/g1"));
    expect(posted(calls)).toHaveLength(1);
    expect(posted(calls)[0]?.body).toEqual({
      name: "release crew",
      members: ["a1", "u2"],
      org_id: "o1",
    });
  });

  it("reloads the picker when the organization changes: its AIs replace the old ones, and a pick that is not its member goes", async () => {
    const { calls, user, dialog } = await open({ ...viewer, "POST /groups": json(201, group()) });
    await user.click(box(dialog, "Atlas"));
    await user.click(box(dialog, "Bob Lima"));
    expect(checked(dialog)).toEqual(["a1", "u2"]);

    await user.selectOptions(await orgField(dialog), "o2");

    expect(await within(dialog).findByRole("checkbox", { name: "Lab" })).toBeInTheDocument();
    expect(ids(dialog)).toEqual(["u2", "u3", "a3"]);
    expect(within(dialog).queryByRole("checkbox", { name: "Atlas" })).toBeNull();
    // Atlas, picked for Acme, is not a member of Globex; Bob stays picked.
    expect(checked(dialog)).toEqual(["u2"]);
    const summary = within(dialog).getByRole("list", { name: "Selected members" });
    expect(summary).not.toHaveTextContent("Atlas");

    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Lab"));
    await user.click(create(dialog));
    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toEqual({
      name: "release crew",
      members: ["u2", "a3"],
      org_id: "o2",
    });
  });

  it("clears a refusal that was about another organization when it changes", async () => {
    const { user, dialog } = await open({
      ...viewer,
      "POST /groups": apiErrorWithRefused([{ id: "u2", reason: "not_org_member" }]),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Bob Lima"));
    await user.click(create(dialog));
    await within(dialog).findByRole("alert");

    await user.selectOptions(await orgField(dialog), "o2");
    await waitFor(() => expect(within(dialog).queryByRole("alert")).toBeNull());
    expect(box(dialog, "Bob Lima")).not.toHaveAttribute("aria-invalid", "true");
  });

  it("names the user the server says is not a member of the organization", async () => {
    const { calls, user, dialog } = await open({
      ...viewer,
      "POST /groups": apiErrorWithRefused([{ id: "u2", reason: "not_org_member" }]),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Bob Lima"));
    await user.click(box(dialog, "Atlas"));
    await user.click(create(dialog));
    const alert = await within(dialog).findByRole("alert");
    const refused = within(alert).getAllByRole("listitem");
    expect(refused).toHaveLength(1);
    expect(refused[0]).toHaveTextContent("Bob Lima");
    expect(refused[0]).toHaveTextContent("not a member of Acme");
    expect(box(dialog, "Bob Lima")).toHaveAttribute("aria-invalid", "true");
    expect(box(dialog, "Atlas")).not.toHaveAttribute("aria-invalid", "true");
    expect(posted(calls)).toHaveLength(1);
  });

  it("speaks Portuguese: the field, its hint and the refusal that names the organization", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    mocks({
      ...viewer,
      "GET /me": json(200, makeMe({ locale: "pt-BR" })),
      "POST /groups": apiErrorWithRefused([{ id: "u2", reason: "not_org_member" }]),
    });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(await screen.findByRole("button", { name: "Novo grupo" }));
    const dialog = await screen.findByRole("dialog", { name: "Novo grupo" });
    const field = await within(dialog).findByRole("combobox", { name: "Organização" });
    expect(optionsOf(field)).toEqual(["Acme", "Globex"]);
    expect(
      within(dialog).getByText("Só quem é membro da organização entra no grupo."),
    ).toBeInTheDocument();
    await user.type(within(dialog).getByRole("textbox", { name: "Nome" }), "equipe");
    await user.click(await within(dialog).findByRole("checkbox", { name: "Bob Lima" }));
    await user.click(within(dialog).getByRole("button", { name: "Criar grupo" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("não é membro de Acme");
  });

  it("tells a viewer the server turns down that only members and admins create groups", async () => {
    const { user, dialog } = await open({
      ...viewer,
      "POST /groups": apiError(403, "forbidden", "forbidden"),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only members and admins of Acme can create groups in it.",
    );
    expect(nameField(dialog)).toHaveValue("release crew");
  });

  it("makes the organization the group was created in the current one, so the group is in the list", async () => {
    const { user, dialog } = await open({ ...viewer, "POST /groups": json(201, group()) });
    await user.selectOptions(await orgField(dialog), "o2");
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    await waitFor(() => expect(useLiveStore.getState().currentOrgId).toBe("o2"));
    expect(window.localStorage.getItem("pl.org")).toBe("o2");
  });

  it("leaves the pick alone when the group is created in the current organization", async () => {
    const { router, user, dialog } = await open({ ...viewer, "POST /groups": json(201, group()) });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/g1"));
    expect(useLiveStore.getState().currentOrgId).toBeNull();
  });

  it("offers no group to a viewer who is a guest everywhere, and says why", async () => {
    const { calls, dialog } = await open(
      {
        ...viewerIn(memberIn("o1", "guest"), memberIn("o3", "guest")),
        "POST /groups": json(201, group()),
      },
      false,
    );
    expect(
      await within(dialog).findByText(
        "You’re a guest in every organization you belong to, and guests can’t create groups.",
      ),
    ).toBeInTheDocument();
    // Nothing to fill in: no name, no organization, no one to pick.
    expect(within(dialog).queryByRole("combobox", { name: "Organization" })).toBeNull();
    expect(within(dialog).queryByRole("textbox", { name: "Name" })).toBeNull();
    expect(within(dialog).queryByRole("checkbox")).toBeNull();
    expect(create(dialog)).toBeDisabled();
    expect(posted(calls)).toHaveLength(0);
  });

  it("shows the members only once the organizations are known, so the list does not jump", async () => {
    let release: () => void = () => {};
    const { dialog } = await open(
      {
        "GET /orgs": () =>
          new Promise<Response>((resolve) => {
            release = () => resolve(viewerIn(memberIn("o2", "admin"))["GET /orgs"].clone());
          }),
      },
      false,
    );
    // Not the whole list of the instance first and Globex's afterwards.
    expect(within(dialog).queryByRole("checkbox")).toBeNull();
    release();
    expect(await within(dialog).findByRole("checkbox", { name: "Lab" })).toBeInTheDocument();
    expect(within(dialog).queryByRole("checkbox", { name: "Atlas" })).toBeNull();
  });

  it("asks nothing about organizations while the viewer's are unknown, and sends none: the server uses their default", async () => {
    const { calls, user, dialog } = await open({
      "GET /orgs": apiError(500, "internal", "boom"),
      "POST /groups": json(201, group()),
    });
    expect(within(dialog).queryByRole("combobox", { name: "Organization" })).toBeNull();
    expect(ids(dialog)).toEqual(["a1", "u2", "u3", "a3", "a2", "a4", "a5"]);
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    await waitFor(() => expect(posted(calls)).toHaveLength(1));
    expect(posted(calls)[0]?.body).toEqual({ name: "release crew", members: [] });
  });

  it("says to create or join an organization first when the server finds the viewer in none", async () => {
    const { user, dialog } = await open({
      "POST /groups": apiError(422, "organization_required", "no organization", {
        org_id: "you belong to no organization",
      }),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(create(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "You’re not in any organization yet. Create or join one first.",
    );
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
      "not available: unknown, disabled, or not in an organization you share",
    );
    expect(refused[1]).toHaveTextContent("Old helper");
    expect(refused[1]).toHaveTextContent("people join through a bridged chat, not as members");
    expect(alert).toHaveTextContent("Nothing was created.");
    expect(router.state.location.pathname).toBe("/");
    expect(dialog).toBeInTheDocument();
    // What the user typed and picked is still there to fix.
    expect(nameField(dialog)).toHaveValue("release crew");
    expect(checked(dialog)).toEqual(["a1", "u2", "a4"]);
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

describe("no kind badges next to names (PROF-09, AD-024)", () => {
  it("shows each picked member by its name alone, an AI as a user", async () => {
    const { user, dialog } = await open();
    await user.click(box(dialog, "Atlas"));
    await user.click(box(dialog, "Bob Lima"));
    const picked = within(dialog).getByRole("list", { name: "Selected members" });
    const [atlas, bob] = within(picked).getAllByRole("listitem") as [HTMLElement, HTMLElement];
    expect(within(atlas).getByText("Atlas")).toBeInTheDocument();
    expect(within(bob).getByText("Bob Lima")).toBeInTheDocument();
    for (const chip of [atlas, bob]) {
      expect(within(chip).queryByTestId("kind-badge")).toBeNull();
      expect(within(chip).queryByText(/^(AI|IA)$/)).toBeNull();
    }
  });

  it("lists a refused AI by its name alone in the list of refused members", async () => {
    const { user, dialog } = await open({
      "POST /groups": apiErrorWithRefused([{ id: "a4", reason: "person" }]),
    });
    await user.type(nameField(dialog), "release crew");
    await user.click(box(dialog, "Old helper"));
    await user.click(create(dialog));
    const alert = await within(dialog).findByRole("alert");
    const refused = within(within(alert).getByRole("listitem"));
    expect(refused.getByText("Old helper")).toBeInTheDocument();
    expect(refused.queryByTestId("kind-badge")).toBeNull();
    expect(refused.queryByText(/^(AI|IA)$/)).toBeNull();
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

  it("says there is no one to add when the user has no contacts", async () => {
    const { dialog } = await open({ "GET /contacts": json(200, makeContacts()) }, false);
    expect(await within(dialog).findByText("No contacts to add yet.")).toBeInTheDocument();
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
