import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useRoom } from "@/entities/room";
import { queryKeys } from "@/shared/api/query-keys";
import type { Room } from "@/shared/api/types.gen";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeRoom } from "@/test/fixtures";
import { LoadedImage } from "@/test/image";
import { useLiveStore } from "@/test/live-store";
import { makeQueryClient, renderWithRouter } from "@/test/render";
import { RoomSettingsSheet } from "@/widgets/room-settings";

const toast = vi.hoisted(() => Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

/** The resize/decode step is mocked here; its own tests live in shared/lib/avatar.test.ts. */
const prepareAvatar = vi.hoisted(() => vi.fn((file: File) => Promise.resolve<Blob>(file)));
vi.mock("@/shared/lib/avatar", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  prepareAvatar,
}));

/*
 * Room settings after AD-015 (CID-22): a room is a conversation of intelligences, so there is no
 * chat bridging, no broadcast switch and no catch-all lock. Saving sends only the fields the
 * sheet still owns.
 */

const room = makeRoom({
  id: "r1",
  name: "deploys",
  description: "",
  instructions: "",
  retention_days: 90,
});

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  vi.clearAllMocks();
});
afterEach(() => vi.unstubAllGlobals());

describe("room settings (CID-22)", () => {
  it("offers no chat settings and saves without them", async () => {
    const api = mockApi({
      "PATCH /rooms/r1": json(200, makeRoom({ ...room, name: "deploys-prod" })),
    });
    const user = userEvent.setup();
    renderWithRouter(
      <RoomSettingsSheet room={room} trigger={<button type="button">Room settings</button>} />,
    );

    await user.click(await screen.findByRole("button", { name: "Room settings" }));
    const sheet = await screen.findByRole("dialog", { name: "Room settings" });
    expect(within(sheet).queryAllByRole("switch")).toHaveLength(0);
    expect(within(sheet).queryByText(/Bridge chats/)).toBeNull();
    expect(within(sheet).queryByText(/Send to every chat/)).toBeNull();
    expect(within(sheet).queryByText(/catch-all/i)).toBeNull();

    const name = within(sheet).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "deploys-prod");
    await user.click(within(sheet).getByRole("button", { name: "Save" }));

    const patches = () => api.calls.filter((call) => call.method === "PATCH");
    await waitFor(() => expect(patches()).toHaveLength(1));
    expect(patches()[0]?.body).toEqual({
      name: "deploys-prod",
      description: "",
      retention_days: 90,
    });
  });
});

/*
 * The photo of a group (GAV-06): the sheet has the editor every picture shares, wired to the group's
 * avatar routes. A chosen file is sent at once, so it is never lost by a Save that does not carry it.
 */

const PHOTO = "/api/v1/rooms/r1/avatar?v=new";
const OLD_PHOTO = "/api/v1/rooms/r1/avatar?v=old";
const png = () => new File([new Uint8Array([137, 80, 78, 71])], "crew.png", { type: "image/png" });

/** The sheet as the page mounts it: reading the room from the cache, so the face follows the room. */
function Sheet({ id }: { id: string }) {
  const found = useRoom(id);
  if (!found.data) return null;
  return (
    <RoomSettingsSheet room={found.data} trigger={<button type="button">Room settings</button>} />
  );
}

function setup(stored: Room, routes: Parameters<typeof mockApi>[0] = {}) {
  let current = stored;
  const api = mockApi({
    [`GET /rooms/${stored.id}`]: () => json(200, current),
    [`PUT /rooms/${stored.id}/avatar`]: () => {
      current = { ...current, avatar_url: PHOTO };
      return json(200, current);
    },
    [`DELETE /rooms/${stored.id}/avatar`]: () => {
      const { avatar_url: _gone, ...rest } = current;
      current = rest;
      return new Response(null, { status: 204 });
    },
    [`PATCH /rooms/${stored.id}`]: () => json(200, current),
    ...routes,
  });
  const queryClient = makeQueryClient();
  queryClient.setQueryData(queryKeys.room(stored.id), stored);
  const user = userEvent.setup();
  renderWithRouter(<Sheet id={stored.id} />, queryClient);
  const calls = (method: string) => api.calls.filter((c) => c.method === method);
  return { user, api, queryClient, calls };
}

async function openSheet(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "Room settings" }));
  return within(await screen.findByRole("dialog", { name: "Room settings" }));
}

const faceOf = (sheet: ReturnType<typeof within>) => sheet.getByTestId("intelligence-avatar");

describe("the photo of a group (GAV-06)", () => {
  beforeEach(() => {
    vi.stubGlobal("Image", LoadedImage);
    URL.createObjectURL = vi.fn(() => "blob:preview-1");
    URL.revokeObjectURL = vi.fn();
  });

  it("offers the photo editor with the group's initials until it has a photo", async () => {
    const { user } = setup(room);
    const sheet = await openSheet(user);
    expect(sheet.getByRole("heading", { name: "Photo" })).toBeInTheDocument();
    expect(sheet.getByLabelText("Upload photo")).toBeInTheDocument();
    expect(faceOf(sheet)).toHaveTextContent("D");
    expect(faceOf(sheet).querySelector("img")).toBeNull();
    expect(sheet.queryByRole("button", { name: "Remove photo" })).toBeNull();
  });

  it("sends the chosen file at once, with no Save, and shows it as the group's photo", async () => {
    const { user, calls, queryClient } = setup(room);
    const sheet = await openSheet(user);
    const file = png();
    await user.upload(sheet.getByLabelText("Upload photo"), file);

    await waitFor(() => expect(calls("PUT")).toHaveLength(1));
    expect(calls("PUT")[0]?.path).toBe("/rooms/r1/avatar");
    expect(calls("PUT")[0]?.body).toBe(file);
    await waitFor(() => expect(faceOf(sheet).querySelector("img")).toHaveAttribute("src", PHOTO));
    expect(queryClient.getQueryData<Room>(queryKeys.room("r1"))?.avatar_url).toBe(PHOTO);
    expect(toast.success).toHaveBeenCalledWith("Photo updated");
    expect(screen.queryByAltText("Preview of the new photo")).toBeNull();
    expect(calls("PATCH")).toHaveLength(0); // the settings were not saved by it
    expect(sheet.getByRole("button", { name: "Remove photo" })).toBeInTheDocument();
  });

  it("resizes a big photo in the browser before it is uploaded", async () => {
    const { user, calls } = setup(room);
    const sheet = await openSheet(user);
    const big = new File([new Uint8Array(3 << 20)], "big.jpg", { type: "image/jpeg" });
    const small = new Blob(["small"], { type: "image/webp" });
    prepareAvatar.mockResolvedValueOnce(small);
    await user.upload(sheet.getByLabelText("Upload photo"), big);

    await waitFor(() => expect(calls("PUT")).toHaveLength(1));
    expect(prepareAvatar).toHaveBeenCalledWith(big);
    expect(calls("PUT")[0]?.body).toBe(small);
  });

  it("keeps the old photo and says why when the server refuses the image", async () => {
    const { user, queryClient } = setup(
      { ...room, avatar_url: OLD_PHOTO },
      { "PUT /rooms/r1/avatar": apiError(422, "avatar_invalid", "avatar invalid") },
    );
    const sheet = await openSheet(user);
    await user.upload(sheet.getByLabelText("Upload photo"), png());

    expect(await sheet.findByRole("alert")).toHaveTextContent(
      "That image can’t be used. Use a PNG, JPEG or WebP of up to 2 MB.",
    );
    expect(sheet.getByAltText("Preview of the new photo")).toHaveAttribute("src", "blob:preview-1");
    expect(queryClient.getQueryData<Room>(queryKeys.room("r1"))?.avatar_url).toBe(OLD_PHOTO);
    expect(sheet.getByRole("button", { name: "Remove photo" })).toBeInTheDocument();
    expect(toast.success).not.toHaveBeenCalled();
  });

  it("names the 2 MB limit when the server answers 413", async () => {
    const { user } = setup(room, {
      "PUT /rooms/r1/avatar": apiError(413, "avatar_invalid", "too large"),
    });
    const sheet = await openSheet(user);
    await user.upload(sheet.getByLabelText("Upload photo"), png());
    expect(await sheet.findByRole("alert")).toHaveTextContent(
      "Use a PNG, JPEG or WebP of up to 2 MB.",
    );
  });

  it("removes the photo, which brings the initials back", async () => {
    const { user, calls, queryClient } = setup({ ...room, avatar_url: OLD_PHOTO });
    const sheet = await openSheet(user);
    await waitFor(() =>
      expect(faceOf(sheet).querySelector("img")).toHaveAttribute("src", OLD_PHOTO),
    );

    await user.click(sheet.getByRole("button", { name: "Remove photo" }));

    await waitFor(() => expect(calls("DELETE")).toHaveLength(1));
    expect(calls("DELETE")[0]?.path).toBe("/rooms/r1/avatar");
    await waitFor(() => expect(faceOf(sheet).querySelector("img")).toBeNull());
    expect(faceOf(sheet)).toHaveTextContent("D");
    expect(queryClient.getQueryData<Room>(queryKeys.room("r1"))?.avatar_url).toBeUndefined();
    expect(toast.success).toHaveBeenCalledWith("Photo removed");
    expect(sheet.queryByRole("button", { name: "Remove photo" })).toBeNull();
    expect(calls("PATCH")).toHaveLength(0); // removing it does not save the form it sits in
  });

  it("does not take the photo away when Enter saves the settings", async () => {
    const { user, calls } = setup({ ...room, avatar_url: OLD_PHOTO });
    const sheet = await openSheet(user);
    await user.type(sheet.getByLabelText("Name"), "{Enter}");

    await waitFor(() => expect(calls("PATCH")).toHaveLength(1));
    expect(calls("DELETE")).toHaveLength(0);
  });
});
