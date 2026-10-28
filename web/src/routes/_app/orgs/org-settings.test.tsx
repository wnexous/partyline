import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import type { Organization } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeMe, makeOrg, makeOrgChart } from "@/test/fixtures";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
// The photo is resized by its own unit tests; the page tests the upload flow itself.
vi.mock("@/shared/lib/avatar", async (importOriginal) => ({ ...(await importOriginal<Record<string, unknown>>()), prepareAvatar: (file: File) => Promise.resolve(file) }));

/*
 * The settings of an organization (T41: OMEM-04, ORG-06, OMEM-05): its name, address and picture,
 * which only its admins change, and the two ways out: leaving it, and deleting it when it owns
 * nothing. Read through the route, which also decides what a non-member sees (CHART-02).
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

const acme = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "admin" });
const asMember = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "member" });
const asGuest = makeOrg({ id: "o1", name: "Acme", slug: "acme", permission: "guest" });
const globex = makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" });

function api(org = acme, over: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe()),
    "GET /orgs": json(200, { items: [org, globex] }),
    "GET /orgs/o1": json(200, org),
    "GET /conversations": json(200, { items: [] }),
    ...over,
  });
}

const page = () => screen.findByRole("heading", { level: 1, name: "Organization settings" });
const name = () => screen.getByRole("textbox", { name: "Name" });
const address = () => screen.getByRole("textbox", { name: "Address" });
const save = () => screen.getByRole("button", { name: "Save" });

beforeEach(() => {
  useLiveStore.setState({ locale: "en", currentOrgId: null });
  window.localStorage.clear();
  URL.createObjectURL = vi.fn(() => "blob:preview-1");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

describe("who may change the organization (OMEM-04)", () => {
  it("shows an admin the name, the address and the picture to change, and both ways out", async () => {
    api();
    renderRoute("/orgs/o1/settings");
    await page();
    expect(name()).toHaveValue("Acme");
    expect(address()).toHaveValue("acme");
    expect(screen.getByLabelText("Upload photo")).toBeInTheDocument();
    expect(save()).toBeDisabled();
    expect(screen.getByRole("button", { name: "Leave Acme" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete organization" })).toBeInTheDocument();
  });

  it.each([
    ["member", asMember],
    ["guest", asGuest],
  ])(
    "shows a %s the same facts with nothing to edit: no field, no upload, no delete",
    async (_, org) => {
      api(org);
      renderRoute("/orgs/o1/settings");
      await page();
      const details = screen.getByRole("region", { name: "Details" });
      expect(within(details).getByText("Acme")).toBeInTheDocument();
      expect(within(details).getByText("acme")).toBeInTheDocument();
      expect(screen.queryByRole("textbox")).toBeNull();
      expect(screen.queryByLabelText("Upload photo")).toBeNull();
      expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
      expect(screen.queryByRole("button", { name: "Delete organization" })).toBeNull();
      expect(screen.queryByRole("region", { name: "Delete organization" })).toBeNull();
      expect(screen.getByText("Only admins of Acme change these.")).toBeInTheDocument();
      // Anyone may still leave.
      expect(screen.getByRole("button", { name: "Leave Acme" })).toBeInTheDocument();
    },
  );

  it("names the organization and the viewer's part in it above every page of it", async () => {
    api(asGuest);
    renderRoute("/orgs/o1/settings");
    await page();
    const bar = screen.getByRole("navigation", { name: "Sections of Acme" });
    expect(within(bar).getByRole("link", { name: "Settings" })).toHaveAttribute(
      "href",
      "/orgs/o1/settings",
    );
    expect(screen.getByTestId("org-bar")).toHaveTextContent("Acme");
    expect(screen.getByTestId("org-bar")).toHaveTextContent("Guest");
  });
});

describe("renaming (OMEM-04)", () => {
  it("lets an admin rename it and shows the new name at once, sending only what changed", async () => {
    const renamed = { ...acme, name: "Acme Inc" };
    const { calls } = api(acme, { "PATCH /orgs/o1": json(200, renamed) });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(name());
    await user.type(name(), "  Acme Inc ");
    expect(save()).toBeEnabled();
    await user.click(save());

    expect(await screen.findByText("Organization saved")).toBeInTheDocument();
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ name: "Acme Inc" });
    expect(screen.getByTestId("org-bar")).toHaveTextContent("Acme Inc");
    expect(name()).toHaveValue("Acme Inc");
    expect(save()).toBeDisabled();
  });

  it("changes the address, in lower case, and sends only that", async () => {
    const { calls } = api(acme, { "PATCH /orgs/o1": json(200, { ...acme, slug: "acme-inc" }) });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(address());
    await user.type(address(), " Acme-Inc ");
    await user.click(save());
    await screen.findByText("Organization saved");
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ slug: "acme-inc" });
    expect(screen.getByTestId("org-bar")).toHaveTextContent("acme-inc");
  });

  it("refuses a blank name and one over 100 characters before asking the server", async () => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(name());
    await user.click(save());
    expect(await screen.findByText("Required")).toBeInTheDocument();

    await user.click(name());
    await user.paste("x".repeat(101));
    await user.click(save());
    expect(await screen.findByText("At most 100 characters")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it.each([
    ["Acme Inc", "a space"],
    ["-acme", "a leading hyphen"],
    ["acme-", "a trailing hyphen"],
    ["ac--me", "a double hyphen"],
    ["acmé", "an accent"],
    ["a".repeat(41), "41 characters"],
  ])("refuses the address %s (%s) before asking the server", async (slug) => {
    const { calls } = api();
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(address());
    await user.type(address(), slug);
    await user.click(save());
    expect(
      await screen.findByText(
        "Use lowercase letters, digits and single hyphens, up to 40 characters.",
      ),
    ).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("accepts an address of 40 characters and one with digits and hyphens", async () => {
    const slug = `${"a".repeat(19)}-${"b1".repeat(10)}`;
    expect(slug).toHaveLength(40);
    const { calls } = api(acme, { "PATCH /orgs/o1": json(200, { ...acme, slug }) });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(address());
    await user.type(address(), slug);
    await user.click(save());
    await screen.findByText("Organization saved");
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ slug });
  });

  it("says an address another organization has is taken, under the field", async () => {
    api(acme, { "PATCH /orgs/o1": apiError(409, "conflict", "slug already taken") });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.clear(address());
    await user.type(address(), "globex");
    await user.click(save());
    expect(await screen.findByText("That address is already taken.")).toBeInTheDocument();
    expect(address()).toHaveValue("globex");
  });

  it("shows the server's reason for a name it refuses (422)", async () => {
    api(acme, {
      "PATCH /orgs/o1": apiError(422, "invalid", "invalid", { name: "must not be blank" }),
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.type(name(), " 2");
    await user.click(save());
    expect(await screen.findByText("must not be blank")).toBeInTheDocument();
  });

  it("tells an admin who was demoted meanwhile that only admins change it (403)", async () => {
    api(acme, { "PATCH /orgs/o1": apiError(403, "forbidden", "forbidden") });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.type(name(), " 2");
    await user.click(save());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Only admins of Acme can change it.",
    );
  });

  it("says so when the server cannot be reached", async () => {
    api(acme, { "PATCH /orgs/o1": () => Promise.reject(new TypeError("offline")) as never });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.type(name(), " 2");
    await user.click(save());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Can’t reach the server. Check your connection and try again.",
    );
  });
});

describe("the picture (OMEM-04)", () => {
  const png = () =>
    new File([new Uint8Array([137, 80, 78, 71])], "logo.png", { type: "image/png" });

  it("sends the chosen file at once, and the new picture shows", async () => {
    const withPhoto = { ...acme, avatar_url: "/api/v1/orgs/o1/avatar?v=abc" };
    const { calls } = api(acme, { "PUT /orgs/o1/avatar": json(200, withPhoto) });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    const file = png();
    await user.upload(screen.getByLabelText("Upload photo"), file);
    await screen.findByText("Photo updated");
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.path).toBe("/orgs/o1/avatar");
    expect(put?.body).toBe(file);
    expect(await screen.findByRole("button", { name: "Remove photo" })).toBeInTheDocument();
  });

  it("shows an image the server cannot use as an error under the picture", async () => {
    api(acme, { "PUT /orgs/o1/avatar": apiError(422, "avatar_invalid", "avatar invalid") });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.upload(screen.getByLabelText("Upload photo"), png());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "That image can’t be used. Use a PNG, JPEG or WebP of up to 2 MB.",
    );
  });

  it("removes the picture and shows the initials again", async () => {
    const withPhoto = { ...acme, avatar_url: "/api/v1/orgs/o1/avatar?v=abc" };
    let org: Organization = withPhoto;
    const { calls } = api(withPhoto, {
      "GET /orgs/o1": () => json(200, org),
      "DELETE /orgs/o1/avatar": () => {
        org = acme;
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.click(await screen.findByRole("button", { name: "Remove photo" }));
    await screen.findByText("Photo removed");
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/orgs/o1/avatar",
    ]);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Remove photo" })).toBeNull());
  });
});

describe("leaving (OMEM-05)", () => {
  const open = async (org = acme, over: Parameters<typeof mockApi>[0] = {}) => {
    const mocks = api(org, over);
    const user = userEvent.setup();
    const view = renderRoute("/orgs/o1/settings");
    await page();
    await user.click(screen.getByRole("button", { name: "Leave Acme" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Leave Acme?" });
    return { ...mocks, ...view, user, dialog };
  };

  it("says what leaving does, and what stays", async () => {
    const { dialog } = await open();
    expect(
      within(dialog).getByText(
        "You leave the groups of Acme and lose your position there. Your account and your other organizations stay.",
      ),
    ).toBeInTheDocument();
  });

  it("leaves with the viewer's own id, goes to the organizations and says so", async () => {
    const { calls, user, dialog, router } = await open(asMember, {
      "DELETE /orgs/o1/members/u1": new Response(null, { status: 204 }),
    });
    await user.click(within(dialog).getByRole("button", { name: "Leave" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual([
      "/orgs/o1/members/u1",
    ]);
    expect(await screen.findByText("You left Acme")).toBeInTheDocument();
  });

  it("tells the only admin to make another member an admin first, and stays open (409 last_admin)", async () => {
    const { user, dialog, router } = await open(acme, {
      "DELETE /orgs/o1/members/u1": apiError(409, "last_admin", "last admin"),
    });
    await user.click(within(dialog).getByRole("button", { name: "Leave" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "You’re the only admin of Acme. Make another member an admin first.",
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/orgs/o1/settings");
  });

  it("says so when it could not leave, and when the server cannot be reached", async () => {
    const { user, dialog } = await open(acme, {
      "DELETE /orgs/o1/members/u1": apiError(500, "internal", "boom"),
    });
    await user.click(within(dialog).getByRole("button", { name: "Leave" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Couldn’t leave Acme. Try again.",
    );
  });

  it("cancels without asking the server", async () => {
    const { calls, user, dialog } = await open();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });
});

describe("deleting (ORG-06)", () => {
  const open = async (over: Parameters<typeof mockApi>[0] = {}) => {
    const mocks = api(acme, over);
    const user = userEvent.setup();
    const view = renderRoute("/orgs/o1/settings");
    await page();
    await user.click(screen.getByRole("button", { name: "Delete organization" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Acme?" });
    return { ...mocks, ...view, user, dialog };
  };
  const confirm = (dialog: HTMLElement) =>
    within(dialog).getByRole("button", { name: "Delete organization" });

  it("says that it goes for good, with what goes with it, and what has to be gone first", async () => {
    const { dialog } = await open();
    expect(
      within(dialog).getByText(
        "Acme is deleted for good, with its positions and pending invitations. It must not own any AI, connection or group: delete those first. This can’t be undone.",
      ),
    ).toBeInTheDocument();
  });

  it("deletes an empty organization, goes to the organizations and says so", async () => {
    let listed = [acme, globex];
    const { calls, user, dialog, router } = await open({
      "GET /orgs": () => json(200, { items: listed }),
      "DELETE /orgs/o1": () => {
        listed = [globex];
        return new Response(null, { status: 204 });
      },
    });
    await user.click(confirm(dialog));
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs"));
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual(["/orgs/o1"]);
    expect(await screen.findByText("Acme deleted")).toBeInTheDocument();
    expect(await screen.findByRole("listitem", { name: "Globex" })).toBeInTheDocument();
    expect(screen.queryByRole("listitem", { name: "Acme" })).toBeNull();
  });

  it("says it still owns something, and stays open, when the server names no counts (409)", async () => {
    const { user, dialog, router } = await open({
      "DELETE /orgs/o1": apiError(409, "organization_not_empty", "not empty"),
    });
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Acme still owns AIs, connections or groups. Delete them first.",
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/orgs/o1/settings");
  });

  it("names each kind it owns, with its count, and leaves out the ones it has none of", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [acme] }),
      "GET /orgs/o1": json(200, acme),
      "GET /conversations": json(200, { items: [] }),
      "DELETE /orgs/o1": () =>
        new Response(
          JSON.stringify({
            error: {
              code: "organization_not_empty",
              message: "not empty",
              details: { agents: "2", connectors: "1", groups: "3" },
            },
          }),
          { status: 409, headers: { "Content-Type": "application/json" } },
        ),
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.click(screen.getByRole("button", { name: "Delete organization" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Acme?" });
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Acme still owns 2 AIs, 1 connection, and 3 groups. Delete them first.",
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("names only the group when that is all it owns (one counts as one)", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [acme] }),
      "GET /orgs/o1": json(200, acme),
      "GET /conversations": json(200, { items: [] }),
      "DELETE /orgs/o1": () =>
        new Response(
          JSON.stringify({
            error: {
              code: "organization_not_empty",
              message: "not empty",
              details: { agents: "0", connectors: "0", groups: "1" },
            },
          }),
          { status: 409, headers: { "Content-Type": "application/json" } },
        ),
    });
    const user = userEvent.setup();
    renderRoute("/orgs/o1/settings");
    await page();
    await user.click(screen.getByRole("button", { name: "Delete organization" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Acme?" });
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Acme still owns 1 group. Delete them first.",
    );
  });

  it("says only admins delete it when the server refuses (403), and when it fails otherwise", async () => {
    const { user, dialog } = await open({
      "DELETE /orgs/o1": apiError(403, "forbidden", "forbidden"),
    });
    await user.click(confirm(dialog));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Only admins of Acme can delete it.",
    );
  });

  it("cancels without asking the server", async () => {
    const { calls, user, dialog } = await open();
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });
});

describe("the organization's page for someone who cannot see it (CHART-02)", () => {
  it("is not found for a non-member (404), whatever page of it they open", async () => {
    mockApi({
      "GET /me": json(200, makeMe()),
      "GET /orgs": json(200, { items: [globex] }),
      "GET /orgs/o1": apiError(404, "not_found", "not found"),
      "GET /conversations": json(200, { items: [] }),
    });
    renderRoute("/orgs/o1/settings");
    expect(
      await screen.findByRole("heading", { name: "This organization is not on your line" }),
    ).toBeInTheDocument();
    expect(screen.getByText("It was deleted, or you are not a member of it.")).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Organization settings" })).toBeNull();
    expect(screen.getByRole("link", { name: "Back to organizations" })).toHaveAttribute(
      "href",
      "/orgs",
    );
  });

  it("says so, and offers to try again, when the organization cannot be read", async () => {
    api(acme, { "GET /orgs/o1": apiError(500, "internal", "boom") });
    renderRoute("/orgs/o1/settings");
    const text = await screen.findByText("Couldn’t load this organization.");
    expect(
      within(text.closest("[role=alert]") as HTMLElement).getByRole("button", {
        name: "Try again",
      }),
    ).toBeInTheDocument();
  });

  it("opens the same page by the organization's address and keeps the address of its id", async () => {
    api(acme, { "GET /orgs/acme": json(200, acme) });
    const { router } = renderRoute("/orgs/acme/settings");
    await page();
    await waitFor(() => expect(router.state.location.pathname).toBe("/orgs/o1/settings"));
  });

  it("opens /orgs/{id} on the org chart, the first page of an organization", async () => {
    api(acme, { "GET /orgs/o1/chart": json(200, makeOrgChart()) });
    const { router } = renderRoute("/orgs/o1");
    await screen.findByRole("tree", { name: "Org chart of Acme" });
    expect(router.state.location.pathname).toBe("/orgs/o1/chart");
  });
});

describe("from the list of organizations", () => {
  it("links each organization to its settings", async () => {
    api(acme, { "GET /invitations": json(200, { items: [] }) });
    renderRoute("/orgs");
    const item = await screen.findByRole("listitem", { name: "Globex" });
    expect(within(item).getByRole("link", { name: "Settings of Globex" })).toHaveAttribute(
      "href",
      "/orgs/o2/settings",
    );
  });
});

describe("in Portuguese and for assistive technology", () => {
  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(acme, { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    renderRoute("/orgs/o1/settings");
    expect(
      await screen.findByRole("heading", { level: 1, name: "Configurações da organização" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("textbox", { name: "Nome" })).toHaveValue("Acme");
    expect(screen.getByRole("textbox", { name: "Endereço" })).toHaveValue("acme");
    expect(screen.getByRole("button", { name: "Sair de Acme" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Excluir organização" })).toBeInTheDocument();
  });

  it("has no axe violations for an admin and for a member, in both locales, and in its dialogs", async () => {
    api(acme);
    const user = userEvent.setup();
    const admin = renderRoute("/orgs/o1/settings");
    await page();
    expect(await axe(admin.container, AXE_OPTIONS)).toHaveNoViolations();

    await user.click(screen.getByRole("button", { name: "Leave Acme" }));
    expect(await axe(await screen.findByRole("alertdialog"), AXE_OPTIONS)).toHaveNoViolations();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());

    await user.click(screen.getByRole("button", { name: "Delete organization" }));
    expect(await axe(await screen.findByRole("alertdialog"), AXE_OPTIONS)).toHaveNoViolations();
    admin.unmount();

    api(asMember);
    const member = renderRoute("/orgs/o1/settings");
    await page();
    expect(await axe(member.container, AXE_OPTIONS)).toHaveNoViolations();
    member.unmount();

    useLiveStore.setState({ locale: "pt-BR" });
    api(acme, { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const pt = renderRoute("/orgs/o1/settings");
    await screen.findByRole("heading", { level: 1, name: "Configurações da organização" });
    expect(await axe(pt.container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
