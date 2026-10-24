import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { queryKeys } from "@/lib/api/queries";
import type { IntelligenceProfile, ProfileMembership } from "@/lib/api/types.gen";
import { handleLiveEvent } from "@/lib/live/handlers";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeAIContact,
  makeContacts,
  makeIntelligence,
  makeMe,
  makeMembership,
  makeOrgAIs,
  makePerson,
  makeProfile,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));
// The photo is resized by its own unit tests; the page tests the upload flow itself.
vi.mock("@/lib/avatar", () => ({ prepareAvatar: (file: File) => Promise.resolve(file) }));

/*
 * The profile page (PROF-03, PROF-05, PROF-11, PEOP-03, ACC-07): what an intelligence is, with an
 * editor for whoever may edit it - the user for themselves, the owner for an AI, the users of a
 * connector for its people.
 */

/** happy-dom never loads images; report them as loaded so the avatar renders its <img>. */
class LoadedImage {
  complete = true;
  naturalWidth = 1;
  crossOrigin: string | null = null;
  referrerPolicy = "";
  src = "";
  addEventListener() {}
  removeEventListener() {}
}

stubLayout();
beforeEach(() => {
  useLiveStore.setState({ locale: "en", presence: {}, openRoomId: null });
  URL.createObjectURL = vi.fn(() => "blob:preview-1");
  URL.revokeObjectURL = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

const me = makeMe();
const ana = makeIntelligence();

const atlasRef = makeAI();
const atlas: IntelligenceProfile = makeProfile({
  ...atlasRef,
  bio: "Release captain.",
  can_edit: true,
  ai: {
    owner: ana,
    folder: "/home/dev/atlas",
    folder_label: "~/atlas",
    app: "claude",
    online: false,
    last_seen_at: "2026-10-19T09:00:00Z",
    my_level: "manage",
  },
  apps: [
    { kind: "claude", label: "Claude Code" },
    { kind: "discord", label: "Discord: @atlasbot", connector_id: "k9" },
  ],
});

const liaRef = makePerson();
const lia: IntelligenceProfile = makeProfile({
  ...liaRef,
  bio: "",
  can_edit: true,
  person: {
    connector: { id: "k1", kind: "whatsapp", name: "ops-line" },
    external_id: "5511999990000",
    name_locked: false,
  },
  apps: [{ kind: "whatsapp", label: "WhatsApp: 5511999990000", connector_id: "k1" }],
  conversations: [
    { id: "r1", name: "support-bridge" },
    { id: "d9", name: "", kind: "dm" },
  ],
});

function api(profile: IntelligenceProfile, extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, me),
    "GET /conversations": json(200, { items: [] }),
    "GET /contacts": json(200, sampleContacts),
    [`GET /intelligences/${profile.id}`]: json(200, profile),
    ...extra,
  });
}

const name = () => screen.findByRole("heading", { level: 1 });
/** The page's header: the photo, the name, the handle and the bio. */
const pageHeader = () => screen.getByRole("heading", { level: 1 }).closest("header") as HTMLElement;
const field = (label: string) => screen.getByRole("textbox", { name: label });

describe("reading a profile (PROF-09)", () => {
  it("shows an AI's name, handle and bio, and where it lives, with no kind badge or row (AD-023)", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-10-19T12:00:00Z"));
    api(atlas);
    renderRoute("/contacts/a1");
    expect(await name()).toHaveTextContent("Atlas");
    const header = pageHeader();
    expect(within(header).queryByText(/^(AI|IA)$/)).toBeNull();
    expect(within(header).queryByTestId("kind-badge")).toBeNull();
    expect(within(header).getByText("@atlas")).toBeInTheDocument();
    expect(within(header).getByText("Release captain.")).toBeInTheDocument();

    const details = screen.getByRole("region", { name: "Details" });
    expect(within(details).queryByText("Kind")).toBeNull();
    expect(within(details).getByText("@ana")).toBeInTheDocument();
    expect(within(details).getByText("Claude Code")).toBeInTheDocument();
    expect(within(details).getByText("Offline")).toBeInTheDocument();
    expect(within(details).getByText("Last seen 3 hr. ago")).toBeInTheDocument();
    // Machines are internal (AD-022): the page shows the folder, not where it lives.
    expect(within(details).queryByText("Machine")).toBeNull();
    expect(within(details).queryByText("devbox")).toBeNull();
    expect(within(details).getByText("~/atlas")).toBeInTheDocument();
    vi.useRealTimers();
  });

  it("says an AI that no folder was ever linked to has none", async () => {
    api({ ...atlas, ai: atlas.ai && { ...atlas.ai, folder: "", folder_label: "" } });
    renderRoute("/contacts/a1");
    await name();
    const details = screen.getByRole("region", { name: "Details" });
    expect(within(details).getByText("No folder yet")).toBeInTheDocument();
    expect(within(details).queryByText("~/atlas")).toBeNull();
  });

  it("follows live presence for an AI", async () => {
    api(atlas);
    const { queryClient } = renderRoute("/contacts/a1");
    const details = await screen.findByRole("region", { name: "Details" });
    expect(within(details).getByText("Offline")).toBeInTheDocument();
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: true },
      }),
    );
    expect(await within(details).findByText("Online")).toBeInTheDocument();
    expect(within(details).queryByText(/Last seen/)).toBeNull();
  });

  it("shows another user as it shows an AI, with no badge, and no AI details", async () => {
    const bob = makeProfile({ id: "u2", display_name: "Bob Lima", handle: "bob", bio: "On call." });
    api(bob);
    renderRoute("/contacts/u2");
    expect(await name()).toHaveTextContent("Bob Lima");
    const header = pageHeader();
    expect(within(header).getByText("@bob")).toBeInTheDocument();
    expect(within(header).queryByTestId("kind-badge")).toBeNull();
    expect(within(header).queryByText(/^(Person|User)$/)).toBeNull();
    expect(screen.queryByRole("region", { name: "Details" })).toBeNull();
  });

  it("marks the viewer's own profile as theirs and offers no message to oneself", async () => {
    api(makeProfile({ can_edit: true }));
    renderRoute("/contacts/u1");
    await name();
    const header = pageHeader();
    expect(within(header).getByText("You")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Message/ })).toBeNull();
  });

  it("says when the contact does not exist or cannot be seen, and goes back to the contacts", async () => {
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /intelligences/zz": apiError(404, "not_found"),
    });
    renderRoute("/contacts/zz");
    expect(
      await screen.findByRole("heading", { name: "This contact is not on your line" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Back to contacts" })).toHaveAttribute(
      "href",
      "/contacts",
    );
  });

  it("shows an error with a way to try again when the profile does not load", async () => {
    let attempts = 0;
    mockApi({
      "GET /me": json(200, me),
      "GET /conversations": json(200, { items: [] }),
      "GET /intelligences/a1": () => {
        attempts += 1;
        return attempts === 1 ? apiError(500, "internal", "boom") : json(200, atlas);
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Couldn’t load this profile.");
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(await name()).toHaveTextContent("Atlas");
  });

  it("links back to the contacts from the page", async () => {
    api(atlas);
    renderRoute("/contacts/a1");
    await name();
    expect(screen.getByRole("link", { name: "Contacts" })).toHaveAttribute("href", "/contacts");
  });
});

describe("messaging from the profile (DM-01)", () => {
  const dm = makeRoom({ id: "d1", kind: "dm", name: "", my_level: "post", member_count: 2 });

  it("opens the DM with the intelligence", async () => {
    const { calls } = api(atlas, {
      "POST /conversations/dm": json(200, dm),
      "GET /rooms/d1": json(200, dm),
      "GET /rooms/d1/members": json(200, { items: [] }),
      "GET /rooms/d1/messages": json(200, { items: [], has_more: false }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Message Atlas" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/rooms/d1"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ with: "a1" });
  });

  it("offers no way to message a person (CID-22)", async () => {
    const { calls } = api(lia);
    renderRoute("/contacts/p1");
    await name();
    expect(screen.queryByRole("button", { name: "Message Lia Costa" })).toBeNull();
    expect(calls.some((c) => c.path === "/conversations/dm")).toBe(false);
  });
});

describe("editing a profile (PROF-03, PROF-05)", () => {
  it("starts from the stored values and keeps Save off until something changes", async () => {
    api(atlas);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    expect(field("Display name")).toHaveValue("Atlas");
    expect(field("Handle")).toHaveValue("atlas");
    expect(field("Bio")).toHaveValue("Release captain.");
    const save = screen.getByRole("button", { name: "Save profile" });
    expect(save).toBeDisabled();
    await user.type(field("Bio"), "!");
    expect(save).toBeEnabled();
    await user.type(field("Bio"), "{Backspace}");
    expect(save).toBeDisabled();
  });

  it("lets the owner rename an AI: only what changed is sent, and the page shows it at once", async () => {
    const renamed = {
      ...atlas,
      display_name: "Atlas Prime",
      handle: "atlas-prime",
      bio: "New bio.",
    };
    const { calls } = api(atlas, { "PATCH /intelligences/a1": json(200, renamed) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Display name"));
    await user.type(field("Display name"), "  Atlas Prime ");
    await user.clear(field("Handle"));
    await user.type(field("Handle"), "@Atlas-Prime");
    await user.clear(field("Bio"));
    await user.type(field("Bio"), "New bio.");
    await user.click(screen.getByRole("button", { name: "Save profile" }));

    await waitFor(() =>
      expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Atlas Prime"),
    );
    expect(calls.find((c) => c.method === "PATCH")).toMatchObject({
      path: "/intelligences/a1",
      body: { display_name: "Atlas Prime", handle: "atlas-prime", bio: "New bio." },
    });
    expect(await screen.findByText("Profile saved")).toBeInTheDocument();
    // The form now holds what was saved, so Save is off again.
    expect(field("Display name")).toHaveValue("Atlas Prime");
    expect(screen.getByRole("button", { name: "Save profile" })).toBeDisabled();
  });

  it("sends only the field that changed", async () => {
    const { calls } = api(atlas, {
      "PATCH /intelligences/a1": json(200, { ...atlas, bio: "Shipping." }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Bio"));
    await user.type(field("Bio"), "Shipping.");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await screen.findByText("Profile saved", undefined, { timeout: 5000 });
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ bio: "Shipping." });
  });

  it("can empty the bio", async () => {
    const { calls } = api(atlas, {
      "PATCH /intelligences/a1": json(200, { ...atlas, bio: "" }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Bio"));
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await screen.findByText("Profile saved", undefined, { timeout: 5000 });
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ bio: "" });
  });

  it("lets a person edit their own account", async () => {
    const { calls } = api(makeProfile({ can_edit: true, bio: "Hi" }), {
      "PATCH /intelligences/u1": json(
        200,
        makeProfile({ can_edit: true, bio: "Hi", display_name: "Ana S." }),
      ),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/u1");
    await name();
    await user.clear(field("Display name"));
    await user.type(field("Display name"), "Ana S.");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await screen.findByText("Profile saved", undefined, { timeout: 5000 });
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ display_name: "Ana S." });
  });

  it("shows no editor to someone who cannot edit", async () => {
    api({ ...atlas, can_edit: false });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("textbox", { name: "Display name" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
    expect(screen.queryByLabelText("Upload photo")).toBeNull();
    expect(screen.queryByRole("heading", { name: "Edit profile" })).toBeNull();
    // They still read it all.
    expect(screen.getByText("Release captain.")).toBeInTheDocument();
  });

  it("refuses an empty name, a bad handle and a long bio before asking the server", async () => {
    const { calls } = api(atlas);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Display name"));
    await user.clear(field("Handle"));
    await user.type(field("Handle"), "1 bad handle");
    await user.clear(field("Bio"));
    await user.click(field("Bio"));
    await user.paste("x".repeat(281));
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText("Required")).toBeInTheDocument();
    expect(
      screen.getByText("Start with a letter; then letters, digits or hyphens, up to 40."),
    ).toBeInTheDocument();
    expect(screen.getByText("At most 280 characters")).toBeInTheDocument();
    expect(field("Display name")).toHaveAttribute("aria-invalid", "true");
    expect(field("Handle")).toHaveAttribute("aria-invalid", "true");
    expect(field("Bio")).toHaveAttribute("aria-invalid", "true");
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
  });

  it("counts the characters of the bio in code points, as the server does", async () => {
    api(atlas);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    expect(screen.getByText("16/280")).toBeInTheDocument();
    await user.clear(field("Bio"));
    await user.type(field("Bio"), "ab😀");
    expect(screen.getByText("3/280")).toBeInTheDocument();
  });

  it("accepts the limits: 64 characters of name, 40 of handle, 280 of bio", async () => {
    const { calls } = api(atlas, { "PATCH /intelligences/a1": json(200, atlas) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Display name"));
    await user.click(field("Display name"));
    await user.paste("n".repeat(64));
    await user.clear(field("Handle"));
    await user.click(field("Handle"));
    await user.paste(`a${"b".repeat(39)}`);
    await user.clear(field("Bio"));
    await user.click(field("Bio"));
    await user.paste("x".repeat(280));
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({
      display_name: "n".repeat(64),
      handle: `a${"b".repeat(39)}`,
      bio: "x".repeat(280),
    });
  });

  it("refuses a name of 65 characters", async () => {
    const { calls } = api(atlas);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Display name"));
    await user.click(field("Display name"));
    await user.paste("n".repeat(65));
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText("At most 64 characters")).toBeInTheDocument();
    expect(calls.some((c) => c.method === "PATCH")).toBe(false);
  });
});

describe("what the server refuses", () => {
  it("shows a taken handle under the handle and keeps the rest as typed", async () => {
    api(atlas, { "PATCH /intelligences/a1": apiError(409, "handle_taken", "handle taken") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Handle"));
    await user.type(field("Handle"), "bob");
    await user.clear(field("Bio"));
    await user.type(field("Bio"), "Changed.");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText("That handle is already taken.")).toBeInTheDocument();
    expect(field("Handle")).toHaveAttribute("aria-invalid", "true");
    expect(field("Handle")).toHaveValue("bob");
    expect(field("Bio")).toHaveValue("Changed.");
    // The header still shows what is stored.
    expect(screen.getByText("@atlas", { selector: "header *" })).toBeInTheDocument();
  });

  it("clears the handle error when the handle is edited again", async () => {
    api(atlas, { "PATCH /intelligences/a1": apiError(409, "handle_taken", "handle taken") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Handle"));
    await user.type(field("Handle"), "bob");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await screen.findByText("That handle is already taken.");
    await user.type(field("Handle"), "2");
    await waitFor(() => expect(screen.queryByText("That handle is already taken.")).toBeNull());
  });

  it("shows the server's reasons for each field", async () => {
    api(atlas, {
      "PATCH /intelligences/a1": apiError(422, "invalid", "invalid", {
        display_name: "must have 1 to 64 characters",
        handle: "is reserved",
        bio: "must have at most 280 characters",
      }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.type(field("Bio"), "!");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByText("must have 1 to 64 characters")).toBeInTheDocument();
    expect(screen.getByText("is reserved")).toBeInTheDocument();
    expect(screen.getByText("must have at most 280 characters")).toBeInTheDocument();
  });

  it("says so when the server refuses the edit, or cannot be reached", async () => {
    api(atlas, { "PATCH /intelligences/a1": apiError(403, "forbidden", "forbidden") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.type(field("Bio"), "!");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("You can’t edit this profile.");
  });

  it("reports an unreachable server as such", async () => {
    api(atlas, {
      "PATCH /intelligences/a1": () => {
        throw new TypeError("failed to fetch");
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.type(field("Bio"), "!");
    await user.click(screen.getByRole("button", { name: "Save profile" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Can’t reach the server.");
  });
});

describe("the form follows changes made elsewhere", () => {
  it("keeps what is typed from the moment the page appears: nothing resets the form on mount", async () => {
    api(atlas);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.clear(field("Bio"));
    await user.type(field("Bio"), "Typed at once");
    await new Promise((resolve) => setTimeout(resolve, 60));
    expect(field("Bio")).toHaveValue("Typed at once");
    expect(screen.getByRole("button", { name: "Save profile" })).toBeEnabled();
  });

  it("keeps what is typed when the same profile is fetched again", async () => {
    api(atlas);
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/contacts/a1");
    await name();
    await user.type(field("Bio"), " More.");
    await act(() => queryClient.invalidateQueries({ queryKey: queryKeys.intelligence("a1") }));
    await new Promise((resolve) => setTimeout(resolve, 60));
    expect(field("Bio")).toHaveValue("Release captain. More.");
    expect(screen.getByRole("button", { name: "Save profile" })).toBeEnabled();
  });

  it("shows a rename that arrives live while nothing was edited", async () => {
    api(atlas);
    const { queryClient } = renderRoute("/contacts/a1");
    await name();
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "intelligence.changed",
        data: { ...atlasRef, display_name: "Atlas Two", handle: "atlas-two" },
      }),
    );
    await waitFor(() => expect(field("Display name")).toHaveValue("Atlas Two"));
    expect(field("Handle")).toHaveValue("atlas-two");
    expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Atlas Two");
  });

  it("leaves what the user is typing alone when a change arrives", async () => {
    api(atlas);
    const user = userEvent.setup();
    const { queryClient } = renderRoute("/contacts/a1");
    await name();
    await user.type(field("Bio"), " More.");
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "intelligence.changed",
        data: { ...atlasRef, display_name: "Atlas Two" },
      }),
    );
    await waitFor(() =>
      expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Atlas Two"),
    );
    expect(field("Bio")).toHaveValue("Release captain. More.");
  });
});

describe("the photo (PROF-03, PROF-06, PROF-07)", () => {
  const png = () => new File([new Uint8Array([137, 80, 78, 71])], "me.png", { type: "image/png" });
  /** The photo the avatar of the page's header shows, if it has one. */
  const headerPhoto = () =>
    within(pageHeader()).getByTestId("intelligence-avatar").querySelector("img");

  beforeEach(() => vi.stubGlobal("Image", LoadedImage));

  it("shows initials when there is no photo, and offers no removal", async () => {
    api(atlas);
    renderRoute("/contacts/a1");
    await name();
    const editor = screen.getByRole("region", { name: "Edit profile" });
    expect(within(editor).getAllByTestId("intelligence-avatar")[0]).toHaveTextContent("A");
    expect(within(editor).queryByRole("button", { name: "Remove photo" })).toBeNull();
  });

  it("sends the chosen file at once, and the new photo shows in the header", async () => {
    const withPhoto = { ...atlas, avatar_url: "/api/v1/intelligences/a1/avatar?v=abc" };
    const { calls } = api(atlas, { "PUT /intelligences/a1/avatar": json(200, withPhoto) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    const file = png();
    await user.upload(screen.getByLabelText("Upload photo"), file);

    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.path).toBe("/intelligences/a1/avatar");
    expect(put?.body).toBe(file);
    await screen.findByText("Photo updated");
    // The preview is gone, and the new avatar shows in the header.
    await waitFor(() => expect(screen.queryByAltText("Preview of the new photo")).toBeNull());
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview-1");
    expect(headerPhoto()).toHaveAttribute("src", "/api/v1/intelligences/a1/avatar?v=abc");
    expect(await screen.findByRole("button", { name: "Remove photo" })).toBeInTheDocument();
  });

  it("shows an image the server cannot use as an error under the photo, keeping the preview and the old photo", async () => {
    const old = { ...atlas, avatar_url: "/api/v1/intelligences/a1/avatar?v=old" };
    api(old, { "PUT /intelligences/a1/avatar": apiError(422, "avatar_invalid", "avatar invalid") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.upload(screen.getByLabelText("Upload photo"), png());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "That image can’t be used. Use a PNG, JPEG or WebP of up to 2 MB.",
    );
    // Nothing was lost: the preview stays so the user sees what failed, and the old photo stays.
    expect(screen.getByAltText("Preview of the new photo")).toBeInTheDocument();
    expect(headerPhoto()).toHaveAttribute("src", "/api/v1/intelligences/a1/avatar?v=old");
  });

  it("says a file over the instance's upload limit is too large", async () => {
    api(atlas, { "PUT /intelligences/a1/avatar": apiError(413, "too_large", "too large") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.upload(screen.getByLabelText("Upload photo"), png());
    expect(await screen.findByRole("alert")).toHaveTextContent("That file is too large.");
  });

  it("says so when the upload fails for another reason", async () => {
    api(atlas, { "PUT /intelligences/a1/avatar": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.upload(screen.getByLabelText("Upload photo"), png());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn’t upload the photo. Try again.",
    );
  });

  it("shows the sending state while the photo is on its way, and sends it once", async () => {
    let release: () => void = () => {};
    const { calls } = api(atlas, {
      "PUT /intelligences/a1/avatar": () =>
        new Promise<Response>((resolve) => {
          release = () => resolve(json(200, atlas));
        }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.upload(screen.getByLabelText("Upload photo"), png());
    const editor = screen.getByRole("region", { name: "Edit profile" });
    expect(await within(editor).findByRole("status")).toHaveTextContent("Sending…");
    expect(screen.getByAltText("Preview of the new photo")).toBeInTheDocument();
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
    release();
    await waitFor(() => expect(screen.queryByAltText("Preview of the new photo")).toBeNull());
  });

  it("removes the photo, and the initials come back", async () => {
    // The server forgets the photo: the refetch that follows must not bring it back.
    let stored: IntelligenceProfile = {
      ...atlas,
      avatar_url: "/api/v1/intelligences/a1/avatar?v=abc",
    };
    const { calls } = api(stored, {
      "GET /intelligences/a1": () => json(200, stored),
      "DELETE /intelligences/a1/avatar": () => {
        stored = atlas;
        return new Response(null, { status: 204 });
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    const header = pageHeader();
    expect(headerPhoto()).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Remove photo" }));
    await screen.findByText("Photo removed");
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/intelligences/a1/avatar");
    await waitFor(() => expect(headerPhoto()).toBeNull());
    expect(within(header).getByTestId("intelligence-avatar")).toHaveTextContent("A");
    expect(screen.queryByRole("button", { name: "Remove photo" })).toBeNull();
  });

  it("says so when the photo could not be removed", async () => {
    const withPhoto = { ...atlas, avatar_url: "/api/v1/intelligences/a1/avatar?v=abc" };
    api(withPhoto, { "DELETE /intelligences/a1/avatar": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await name();
    await user.click(await screen.findByRole("button", { name: "Remove photo" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn’t remove the photo. Try again.",
    );
    expect(screen.getByRole("button", { name: "Remove photo" })).toBeInTheDocument();
  });

  it("only offers photo files in the picker", async () => {
    api(atlas);
    renderRoute("/contacts/a1");
    await name();
    expect(screen.getByLabelText("Upload photo")).toHaveAttribute(
      "accept",
      "image/png,image/jpeg,image/webp",
    );
  });
});

describe("a person (PEOP-03, PROF-11)", () => {
  it("shows the name, the platform account it writes from and who it is linked to", async () => {
    api({
      ...lia,
      person: {
        ...(lia.person as NonNullable<IntelligenceProfile["person"]>),
        linked_user: makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" }),
      },
    });
    renderRoute("/contacts/p1");
    expect(await name()).toHaveTextContent("Lia Costa");
    const header = pageHeader();
    expect(within(header).queryByText("WhatsApp")).toBeNull();
    expect(within(header).queryByTestId("kind-badge")).toBeNull();
    const details = screen.getByRole("region", { name: "Details" });
    expect(within(details).getByText("WhatsApp")).toBeInTheDocument();
    expect(within(details).getByText("ops-line")).toBeInTheDocument();
    expect(within(details).getByText("5511999990000")).toBeInTheDocument();
    const linked = within(details).getByRole("link", { name: /Bob Lima/ });
    expect(linked).toHaveAttribute("href", "/contacts/u2");
  });

  it("lists the conversations the viewer can see, each opening it", async () => {
    api(lia);
    renderRoute("/contacts/p1");
    await name();
    const list = screen.getByRole("list", { name: "Conversations" });
    const links = within(list).getAllByRole("link");
    expect(links.map((a) => a.getAttribute("href"))).toEqual(["/rooms/r1", "/rooms/d9"]);
    expect(links[0]).toHaveTextContent("support-bridge");
    // A direct message has no name of its own.
    expect(links[1]).toHaveTextContent("Direct message");
  });

  it("says there are none when the viewer can see no conversation with them", async () => {
    api({ ...lia, conversations: [] });
    renderRoute("/contacts/p1");
    await name();
    expect(screen.getByText("No conversation you can see yet.")).toBeInTheDocument();
    expect(screen.queryByRole("list", { name: "Conversations" })).toBeNull();
  });

  it("shows a person read-only: no editor and no way to message them (CID-21, CID-22)", async () => {
    api(lia);
    renderRoute("/contacts/p1");
    await name();
    // The page would offer the editor from can_edit; a person is history now (T24).
    expect(screen.queryByLabelText("Display name")).toBeNull();
    expect(screen.queryByRole("button", { name: "Save profile" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Message Lia Costa" })).toBeNull();
    // Reading stays: the account it writes from is still there, in the details.
    expect(
      within(screen.getByRole("region", { name: "Details" })).getByText("WhatsApp"),
    ).toBeInTheDocument();
  });

  it("shows only the platform to someone who sees them through a conversation, with no editor", async () => {
    api({
      ...lia,
      can_edit: false,
      person: undefined,
      apps: [{ kind: "whatsapp", label: "WhatsApp" }],
    });
    renderRoute("/contacts/p1");
    await name();
    expect(screen.queryByRole("textbox", { name: "Display name" })).toBeNull();
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("WhatsApp")).toBeInTheDocument();
    expect(within(apps).queryByRole("link")).toBeNull();
    // Without the connector there is no account to show.
    expect(screen.queryByText("5511999990000")).toBeNull();
  });
});

describe("apps (ACC-07)", () => {
  it("lists where an AI speaks, including the connectors it operates, each linking to its connector", async () => {
    api(atlas);
    renderRoute("/contacts/a1");
    await name();
    const apps = screen.getByRole("region", { name: "Apps" });
    const items = within(apps).getAllByRole("listitem");
    expect(items.map((li) => li.textContent)).toEqual([
      expect.stringContaining("Claude Code"),
      expect.stringContaining("Discord: @atlasbot"),
    ]);
    expect(
      within(items[1] as HTMLElement).getByRole("link", { name: /Discord: @atlasbot/ }),
    ).toHaveAttribute("href", "/connectors/k9");
    expect(within(items[0] as HTMLElement).queryByRole("link")).toBeNull();
  });

  it("lists a user's web app and the chat accounts verified as theirs", async () => {
    api(
      makeProfile({
        can_edit: true,
        apps: [
          { kind: "web", label: "Web app" },
          { kind: "whatsapp", label: "WhatsApp: 5511999990000", connector_id: "k1" },
        ],
      }),
    );
    renderRoute("/contacts/u1");
    await name();
    const apps = screen.getByRole("region", { name: "Apps" });
    expect(within(apps).getByText("Web app")).toBeInTheDocument();
    expect(within(apps).getByText("WhatsApp: 5511999990000")).toBeInTheDocument();
  });

  it("leaves the section out when there is nothing to list", async () => {
    api({ ...atlas, apps: [] });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("region", { name: "Apps" })).toBeNull();
  });
});

/*
 * What the viewer may do about an identity comes from its profile (POS-04, POS-05, POS-06): the
 * server says, for each organization they share, whether they may delete it there and what that
 * does, and whether they may end its session. The page offers exactly those, and nothing is shared
 * one by one any more (ORG-02).
 */
const acme = (over: Partial<ProfileMembership> = {}) =>
  makeMembership({ org_id: "o1", org_name: "acme", org_slug: "acme", ...over });
const globex = (over: Partial<ProfileMembership> = {}) =>
  makeMembership({ org_id: "o2", org_name: "Globex", org_slug: "globex", ...over });

/** Atlas, an AI of acme that the viewer manages: they may delete it there. */
const atlasInAcme: IntelligenceProfile = {
  ...atlas,
  memberships: [acme({ can_delete: true, delete_effect: "delete" })],
};
const bobRef = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
/** Bob, a person the viewer shares acme with: they may remove him from it. */
const bobInAcme: IntelligenceProfile = makeProfile({
  ...bobRef,
  memberships: [acme({ permission: "member", can_delete: true, delete_effect: "remove" })],
});
const noContent = () => new Response(null, { status: 204 });
const sectionOf = (title: string) => screen.getByRole("region", { name: title });

describe("the organizations of an identity (OMEM-03)", () => {
  it("lists each organization the viewer shares with it: the permission and the position", async () => {
    api(
      makeProfile({
        ...bobRef,
        memberships: [
          acme({ permission: "member", position_id: "p1", position_name: "Lead developer" }),
          globex({ permission: "guest" }),
          makeMembership({
            org_id: "o3",
            org_name: "Initech",
            org_slug: "initech",
            permission: "admin",
          }),
        ],
      }),
    );
    renderRoute("/contacts/u2");
    await name();
    const list = within(sectionOf("Organizations")).getByRole("list");
    const rows = within(list).getAllByRole("listitem");
    expect(rows.map((r) => r.getAttribute("aria-label"))).toEqual(["acme", "Globex", "Initech"]);
    expect(rows[0]).toHaveTextContent("Member");
    expect(rows[0]).toHaveTextContent("Lead developer");
    expect(rows[1]).toHaveTextContent("Guest");
    expect(rows[1]).not.toHaveTextContent("Lead developer");
    expect(rows[2]).toHaveTextContent("Admin");
  });

  it("marks the organization an AI belongs to as its home", async () => {
    api({
      ...atlas,
      memberships: [
        acme({ delete_effect: "delete" }),
        globex({ permission: "guest", delete_effect: "remove" }),
      ],
    });
    renderRoute("/contacts/a1");
    await name();
    expect(
      within(screen.getByRole("listitem", { name: "acme" })).getByText("Home organization"),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole("listitem", { name: "Globex" })).queryByText("Home organization"),
    ).toBeNull();
  });

  it("shows no organizations for an identity the viewer shares none with", async () => {
    api(lia);
    renderRoute("/contacts/p1");
    await name();
    expect(screen.queryByRole("region", { name: "Organizations" })).toBeNull();
  });

  it("lists the viewer's own organizations on their own profile, with nothing to do about them", async () => {
    api(
      makeProfile({
        can_edit: true,
        memberships: [acme({ permission: "admin" }), globex()],
      }),
    );
    renderRoute("/contacts/u1");
    await name();
    expect(within(sectionOf("Organizations")).getAllByRole("listitem")).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /^(Delete|Remove) / })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(
      makeProfile({
        ...bobRef,
        memberships: [
          acme({ permission: "guest", can_delete: true, delete_effect: "remove" }),
          globex({ permission: "admin", position_name: "Diretoria" }),
        ],
      }),
      { "GET /me": json(200, makeMe({ locale: "pt-BR" })) },
    );
    renderRoute("/contacts/u2");
    await name();
    const section = sectionOf("Organizações");
    expect(within(section).getByRole("listitem", { name: "acme" })).toHaveTextContent("Convidado");
    expect(within(section).getByRole("listitem", { name: "Globex" })).toHaveTextContent(
      "Administrador",
    );
    expect(within(section).getByRole("listitem", { name: "Globex" })).toHaveTextContent(
      "Diretoria",
    );
    expect(within(section).getByRole("button", { name: "Remover de acme" })).toBeInTheDocument();
  });
});

describe("what the viewer may do, from the flags of the profile (POS-04, POS-05, POS-06)", () => {
  it("offers no action to someone who may do none", async () => {
    api({
      ...atlas,
      can_edit: false,
      can_end_session: false,
      ai: atlas.ai && { ...atlas.ai, online: true, my_level: "use" },
      memberships: [acme({ delete_effect: "delete" }), globex({ delete_effect: "remove" })],
    });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: "End session" })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Delete / })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Remove / })).toBeNull();
    expect(screen.queryByRole("button", { name: /^Merge / })).toBeNull();
    expect(screen.queryByRole("textbox", { name: "Display name" })).toBeNull();
    // They can still talk to it.
    expect(screen.getByRole("button", { name: "Message Atlas" })).toBeInTheDocument();
  });

  it("offers Delete for the organization an AI belongs to and Remove from for another one", async () => {
    api({
      ...atlas,
      memberships: [
        acme({ can_delete: true, delete_effect: "delete" }),
        globex({ permission: "guest", can_delete: true, delete_effect: "remove" }),
      ],
    });
    renderRoute("/contacts/a1");
    await name();
    const home = screen.getByRole("listitem", { name: "acme" });
    const other = screen.getByRole("listitem", { name: "Globex" });
    expect(within(home).getByRole("button", { name: "Delete Atlas" })).toBeInTheDocument();
    expect(within(home).queryByRole("button", { name: /^Remove / })).toBeNull();
    expect(within(other).getByRole("button", { name: "Remove from Globex" })).toBeInTheDocument();
    expect(within(other).queryByRole("button", { name: /^Delete / })).toBeNull();
  });

  it("offers an action only for the organizations where the server allows it", async () => {
    api({
      ...atlas,
      memberships: [
        acme({ can_delete: false, delete_effect: "delete" }),
        globex({ can_delete: true, delete_effect: "remove" }),
      ],
    });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: "Delete Atlas" })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove from Globex" })).toBeInTheDocument();
  });

  it("offers Edit by the editor of whoever may edit it, with or without a delete", async () => {
    api({ ...atlas, memberships: [acme({ can_delete: false, delete_effect: "delete" })] });
    renderRoute("/contacts/a1");
    await name();
    expect(field("Display name")).toHaveValue("Atlas");
  });

  it("has no machine row: the folder is all it says about where an AI lives (AD-022)", async () => {
    api(atlasInAcme);
    renderRoute("/contacts/a1");
    await name();
    const details = sectionOf("Details");
    expect(within(details).queryByText("Machine")).toBeNull();
    expect(within(details).getByText("Folder")).toBeInTheDocument();
    expect(within(details).getByText("~/atlas")).toBeInTheDocument();
  });

  it("says who hosts an AI, not who owns it: an organization does", async () => {
    api(atlasInAcme);
    renderRoute("/contacts/a1");
    await name();
    const details = sectionOf("Details");
    expect(within(details).getByText("Hosted by")).toBeInTheDocument();
    expect(within(details).queryByText("Owner")).toBeNull();
  });

  it("offers no way to share it one by one, to anyone: organizations are the only way to share", async () => {
    api(atlasInAcme);
    const manager = renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: /Share/ })).toBeNull();
    manager.unmount();

    api({ ...atlasInAcme, can_edit: false, ai: atlas.ai && { ...atlas.ai, my_level: "use" } });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: /Share/ })).toBeNull();
  });
});

describe("ending a session (POS-04)", () => {
  const online = (over: Partial<IntelligenceProfile> = {}): IntelligenceProfile => ({
    ...atlas,
    can_end_session: true,
    ai: atlas.ai && { ...atlas.ai, online: true },
    ...over,
  });

  it("asks first, then ends the session of an AI that is online", async () => {
    const { calls } = api(online(), { "DELETE /agents/a1/session": noContent });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "End session" }));
    const dialog = await screen.findByRole("alertdialog", { name: "End Atlas’s session?" });
    expect(dialog).toHaveTextContent(
      "The session stops and Atlas goes offline. Atlas is not deleted and can connect again.",
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "End session" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/agents/a1/session");
    expect(await screen.findByText("Atlas’s session ended")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
  });

  it("does nothing when the dialog is cancelled", async () => {
    const { calls } = api(online(), { "DELETE /agents/a1/session": noContent });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "End session" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("says so, and keeps the dialog, when the session could not be ended", async () => {
    api(online(), { "DELETE /agents/a1/session": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "End session" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "End session" }));
    expect(await screen.findByText("Couldn’t end Atlas’s session.")).toBeInTheDocument();
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("offers it only while the AI is online, and follows live presence", async () => {
    api(online({ ai: atlas.ai && { ...atlas.ai, online: false } }));
    const { queryClient } = renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: "End session" })).toBeNull();
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: true },
      }),
    );
    expect(await screen.findByRole("button", { name: "End session" })).toBeInTheDocument();
    act(() =>
      handleLiveEvent(queryClient, useLiveStore.getState(), {
        type: "agent.presence",
        data: { agent_id: "a1", online: false },
      }),
    );
    await waitFor(() => expect(screen.queryByRole("button", { name: "End session" })).toBeNull());
  });

  it("offers it to nobody the server did not allow, online or not", async () => {
    api(online({ can_end_session: false }));
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: "End session" })).toBeNull();
  });

  it("offers it on no profile but an AI's", async () => {
    api({ ...bobInAcme, can_end_session: true });
    renderRoute("/contacts/u2");
    await name();
    expect(screen.queryByRole("button", { name: "End session" })).toBeNull();
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(online(), { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Encerrar sessão" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Encerrar a sessão de Atlas?" });
    expect(dialog).toHaveTextContent("A sessão para e Atlas fica offline.");
  });
});

describe("deleting an AI in its organization (POS-05, POS-06)", () => {
  it("deletes the AI after confirmation and returns to the contacts", async () => {
    const { calls } = api(atlasInAcme, {
      "DELETE /agents/a1": noContent,
      "GET /contacts": json(200, { me: ana, org_ais: [], hosted: [], users: [], people: [] }),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Delete Atlas" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Atlas?" });
    expect(dialog).toHaveTextContent(
      "It leaves every group and organization, its direct messages are deleted and the connections it operates are left without an operator. A session that starts again in its folder creates a new AI.",
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts"));
    expect(calls.filter((c) => c.method === "DELETE").map((c) => c.path)).toEqual(["/agents/a1"]);
  });

  it("says an organization cannot be left without an admin when the AI is its only one", async () => {
    api(atlasInAcme, {
      "DELETE /agents/a1": apiError(409, "last_admin", "organization would have no admin"),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Delete Atlas" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Atlas?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    expect(
      await screen.findByText(
        "Atlas is the only admin of an organization. Make another member an admin first.",
      ),
    ).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/contacts/a1");
  });

  it("says so when the AI could not be deleted", async () => {
    api(atlasInAcme, { "DELETE /agents/a1": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Delete Atlas" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Atlas?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete AI" }));
    expect(await screen.findByText("Couldn’t delete Atlas.")).toBeInTheDocument();
  });
});

describe("removing an identity from an organization (POS-05, POS-06)", () => {
  it("says what it does and what it leaves alone, and asks before sending anything", async () => {
    const { calls } = api(bobInAcme, { "DELETE /orgs/o1/members/u2": noContent });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove Bob Lima from acme?" });
    // Literal wording (L-011): the person leaves this organization and nothing else.
    expect(dialog).toHaveTextContent(
      "Bob Lima leaves the groups of acme and loses their position there. Removing someone only takes them out of acme: their account and their other organizations stay.",
    );
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("removes the person from that organization only, and the page shows it gone", async () => {
    let removed = false;
    const { calls } = api(bobInAcme, {
      "GET /intelligences/u2": () =>
        json(200, removed ? { ...bobInAcme, memberships: [] } : bobInAcme),
      "DELETE /orgs/o1/members/u2": () => {
        removed = true;
        return noContent();
      },
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove from acme" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/orgs/o1/members/u2");
    expect(await screen.findByText("Bob Lima removed from acme")).toBeInTheDocument();
    // The organization is gone from the list; the page stays, because the person still exists.
    await waitFor(() => expect(screen.queryByRole("region", { name: "Organizations" })).toBeNull());
    expect(screen.queryByRole("button", { name: "Remove from acme" })).toBeNull();
    expect(router.state.location.pathname).toBe("/contacts/u2");
    expect(calls.some((c) => c.path === "/agents/u2")).toBe(false);
  });

  it("uses the id of the organization, not its name", async () => {
    const { calls } = api(
      makeProfile({
        ...bobRef,
        memberships: [
          globex({ can_delete: true, delete_effect: "remove" }),
          acme({ can_delete: true, delete_effect: "remove" }),
        ],
      }),
      { "DELETE /orgs/o2/members/u2": noContent },
    );
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from Globex" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove Bob Lima from Globex?" });
    await user.click(within(dialog).getByRole("button", { name: "Remove from Globex" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
    expect(calls.find((c) => c.method === "DELETE")?.path).toBe("/orgs/o2/members/u2");
  });

  it("says an AI of another organization stays in its own", async () => {
    api({
      ...atlas,
      memberships: [
        acme({ delete_effect: "delete" }),
        globex({ permission: "guest", can_delete: true, delete_effect: "remove" }),
      ],
    });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(await screen.findByRole("button", { name: "Remove from Globex" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remove Atlas from Globex?" });
    expect(dialog).toHaveTextContent(
      "Atlas leaves the groups of Globex and loses its position there. Removing an AI from an organization that is not its home only takes it out of Globex: the AI itself stays, in its home organization.",
    );
  });

  it("cancels without sending anything", async () => {
    const { calls } = api(bobInAcme, { "DELETE /orgs/o1/members/u2": noContent });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("names the organization that would be left without an admin, and stays open", async () => {
    api(bobInAcme, {
      "DELETE /orgs/o1/members/u2": apiError(409, "last_admin", "organization would have no admin"),
    });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove from acme" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Bob Lima is the only admin of acme. Make another member an admin first.",
    );
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
  });

  it("says so, and stays open to try again, when it could not be removed", async () => {
    api(bobInAcme, { "DELETE /orgs/o1/members/u2": apiError(500, "internal", "boom") });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove from acme" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Couldn’t remove Bob Lima from acme. Try again.",
    );
    expect(within(dialog).getByRole("button", { name: "Remove from acme" })).toBeEnabled();
  });

  it("reports an unreachable server as such", async () => {
    api(bobInAcme, {
      "DELETE /orgs/o1/members/u2": () => {
        throw new TypeError("failed to fetch");
      },
    });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remove from acme" }));
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Remove from acme" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("Can’t reach the server.");
  });

  it("speaks Portuguese", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    api(bobInAcme, { "GET /me": json(200, makeMe({ locale: "pt-BR" })) });
    const user = userEvent.setup();
    renderRoute("/contacts/u2");
    await user.click(await screen.findByRole("button", { name: "Remover de acme" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Remover Bob Lima de acme?" });
    expect(dialog).toHaveTextContent(
      "Bob Lima sai dos grupos de acme e perde o cargo que tem lá. Remover alguém só tira essa pessoa de acme: a conta e as outras organizações dela continuam.",
    );
  });
});

describe("merging an AI from its profile (MRG-01, MRG-02)", () => {
  const notesProfile: IntelligenceProfile = makeProfile({
    ...makeAI({ id: "a2", display_name: "Notes", handle: "notes" }),
    can_edit: true,
    ai: {
      owner: ana,
      folder: "/home/dev/atlas/notes",
      folder_label: "~/atlas/notes",
      app: "opencode",
      online: false,
      my_level: "manage",
    },
  });

  it("offers it on an offline AI of the owner, merges into a picked AI and opens the survivor", async () => {
    const { calls } = api(atlas, {
      "POST /agents/a1/merge": json(200, makeAgent({ id: "a2", name: "notes" })),
      "GET /intelligences/a2": json(200, notesProfile),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/a1");
    await name();
    await user.click(await screen.findByRole("button", { name: "Merge Atlas into another AI" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge Atlas into…" });
    const picker = within(dialog).getByRole("combobox", { name: "AI" });
    // The user's AIs of the same home organization only: not the one being merged away, none of
    // another organization (Lab, the old helper) and none that another user hosts (Sage).
    expect(
      within(picker)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Pick an AI", "Notes (@notes)"]);
    await user.selectOptions(picker, "a2");
    await user.click(within(dialog).getByRole("button", { name: "Merge AI" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/contacts/a2"));
    expect(calls.find((c) => c.method === "POST")).toMatchObject({
      path: "/agents/a1/merge",
      body: { into: "a2" },
    });
    // The contacts and the conversations are the server's to rebuild after a merge.
    await waitFor(() =>
      expect(
        calls.filter((c) => c.method === "GET" && c.path === "/contacts").length,
      ).toBeGreaterThan(1),
    );
    expect(
      calls.filter((c) => c.method === "GET" && c.path === "/conversations").length,
    ).toBeGreaterThan(1);
    expect(await screen.findByRole("heading", { level: 1 })).toHaveTextContent("Notes");
  });

  it("offers no merge to someone who only uses the AI, and asks for no contacts for them", async () => {
    const { calls } = api({
      ...atlas,
      can_edit: false,
      ai: { ...(atlas.ai as NonNullable<IntelligenceProfile["ai"]>), my_level: "use" },
    });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: /^Merge / })).toBeNull();
    expect(calls.some((c) => c.path === "/contacts")).toBe(false);
  });

  it("offers it neither on a user's profile nor on a person's", async () => {
    api(makeProfile({ can_edit: true }));
    const user = renderRoute("/contacts/u1");
    await name();
    expect(screen.queryByRole("button", { name: /^Merge / })).toBeNull();
    user.unmount();

    api(lia);
    renderRoute("/contacts/p1");
    await screen.findByRole("heading", { level: 1, name: "Lia Costa" });
    expect(screen.queryByRole("button", { name: /^Merge / })).toBeNull();
  });

  it("offers no merge when the owner has no other AI to merge into", async () => {
    api(atlas, {
      "GET /contacts": json(
        200,
        makeContacts({
          org_ais: [makeOrgAIs({ ais: [makeAIContact({ intelligence: atlasRef })] })],
        }),
      ),
    });
    renderRoute("/contacts/a1");
    await name();
    expect(screen.queryByRole("button", { name: /^Merge / })).toBeNull();
  });

  it("asks to end the session first when the AI is online (MRG-02)", async () => {
    api(atlas, {
      "POST /agents/a1/merge": apiError(409, "identity_online", "identity is online"),
    });
    const user = userEvent.setup();
    const { router } = renderRoute("/contacts/a1");
    await name();
    await user.click(await screen.findByRole("button", { name: "Merge Atlas into another AI" }));
    const dialog = await screen.findByRole("dialog", { name: "Merge Atlas into…" });
    await user.selectOptions(within(dialog).getByRole("combobox", { name: "AI" }), "a2");
    await user.click(within(dialog).getByRole("button", { name: "Merge AI" }));

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "End its session first: Atlas is online.",
    );
    // Nothing changed: the dialog stays and the page does not leave for a survivor.
    expect(screen.getByRole("dialog", { name: "Merge Atlas into…" })).toBeInTheDocument();
    expect(router.state.location.pathname).toBe("/contacts/a1");
  });
});
