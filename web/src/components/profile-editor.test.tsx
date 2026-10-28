import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ProfileEditor } from "@/components/profile-editor";
import { IntelligenceAvatar } from "@/entities/intelligence";
import { queryKeys, useIntelligence } from "@/lib/api/queries";
import type { IntelligenceProfile } from "@/lib/api/types.gen";
import { useLiveStore } from "@/lib/live/store";
import { apiError, json, mockApi } from "@/test/fetch";
import { makeProfile } from "@/test/fixtures";
import { makeQueryClient, renderWithProviders } from "@/test/render";

const toast = vi.hoisted(() => Object.assign(vi.fn(), { success: vi.fn(), error: vi.fn() }));
vi.mock("sonner", () => ({ toast }));

/** The resize/decode step is mocked here; its own tests live in lib/avatar.test.ts. */
const prepareAvatar = vi.hoisted(() => vi.fn((file: File) => Promise.resolve<Blob>(file)));
vi.mock("@/lib/avatar", () => ({ prepareAvatar }));

/*
 * The photo of the profile editor (PROF-03, PROF-06, PROF-07): a chosen file is sent as soon as it
 * is chosen, so it is never lost by a Save that does not carry it or by leaving the page. The
 * page's header is a second reader of the same profile, to see the photo change everywhere.
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

const atlas = makeProfile({
  id: "a1",
  display_name: "Atlas",
  handle: "atlas",
  bio: "Release captain.",
  can_edit: true,
});

const PHOTO = "/api/v1/intelligences/a1/avatar?v=new";
const OLD_PHOTO = "/api/v1/intelligences/a1/avatar?v=old";

const png = () => new File([new Uint8Array([137, 80, 78, 71])], "me.png", { type: "image/png" });

/** The editor with the page header's avatar reading the same profile, as the page does. */
function Editor({ id }: { id: string }) {
  const profile = useIntelligence(id);
  if (!profile.data) return null;
  return (
    <>
      <header>
        <IntelligenceAvatar intelligence={profile.data} size="xl" decorative />
      </header>
      <ProfileEditor profile={profile.data} />
    </>
  );
}

function setup(profile: IntelligenceProfile, routes: Parameters<typeof mockApi>[0] = {}) {
  let stored = profile;
  const api = mockApi({
    [`GET /intelligences/${profile.id}`]: () => json(200, stored),
    [`PUT /intelligences/${profile.id}/avatar`]: () => {
      stored = { ...stored, avatar_url: PHOTO };
      return json(200, stored);
    },
    ...routes,
  });
  const queryClient = makeQueryClient();
  queryClient.setQueryData(queryKeys.intelligence(profile.id), profile);
  const user = userEvent.setup();
  renderWithProviders(<Editor id={profile.id} />, queryClient);
  return { user, api, puts: () => api.calls.filter((c) => c.method === "PUT") };
}

const headerPhoto = () =>
  within(screen.getByRole("banner")).getByTestId("intelligence-avatar").querySelector("img");
const picker = () => screen.getByLabelText("Upload photo");

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  vi.stubGlobal("Image", LoadedImage);
  URL.createObjectURL = vi.fn(() => "blob:preview-1");
  URL.revokeObjectURL = vi.fn();
  vi.clearAllMocks();
});
afterEach(() => vi.unstubAllGlobals());

describe("choosing a profile photo (PROF-03, PROF-06)", () => {
  it("sends the chosen file at once, with no other click", async () => {
    const { user, puts } = setup(atlas);
    const file = png();
    await user.upload(picker(), file);

    await waitFor(() => expect(puts()).toHaveLength(1));
    const put = puts()[0];
    expect(put?.path).toBe("/intelligences/a1/avatar");
    expect(put?.body).toBe(file);
    expect(URL.createObjectURL).toHaveBeenCalledWith(file);
    // Nothing left to confirm or cancel.
    expect(screen.queryByRole("button", { name: "Use this photo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
    expect(toast.success).toHaveBeenCalledWith("Photo updated");
  });

  it("shows the new photo everywhere once the server accepts it", async () => {
    const { user } = setup(atlas);
    expect(headerPhoto()).toBeNull();
    await user.upload(picker(), png());

    await waitFor(() => expect(headerPhoto()).toHaveAttribute("src", PHOTO));
    expect(screen.queryByAltText("Preview of the new photo")).toBeNull();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:preview-1");
  });

  it("keeps the old photo and shows the reason when the image is refused", async () => {
    const old = { ...atlas, avatar_url: OLD_PHOTO };
    const { user } = setup(old, {
      "PUT /intelligences/a1/avatar": apiError(422, "avatar_invalid", "avatar invalid"),
    });
    await user.upload(picker(), png());

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(
      "That image can’t be used. Use a PNG, JPEG or WebP of up to 2 MB.",
    );
    // What failed stays in view, and the old photo was not touched.
    expect(screen.getByAltText("Preview of the new photo")).toHaveAttribute(
      "src",
      "blob:preview-1",
    );
    expect(headerPhoto()).toHaveAttribute("src", OLD_PHOTO);
    expect(screen.getByRole("button", { name: "Remove photo" })).toBeInTheDocument();
    expect(toast.success).not.toHaveBeenCalled();
  });

  it("sends the same file again when it is chosen again", async () => {
    const { user, puts } = setup(atlas);
    const file = png();
    await user.upload(picker(), file);
    await waitFor(() => expect(puts()).toHaveLength(1));
    await waitFor(() => expect(screen.queryByAltText("Preview of the new photo")).toBeNull());

    await user.upload(picker(), file);
    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(puts().map((c) => c.body)).toEqual([file, file]);
  });

  it("prepares a big photo before it is uploaded", async () => {
    const { user, puts } = setup(atlas);
    const big = new File([new Uint8Array(5 << 20)], "big.png", { type: "image/png" });
    const small = new File([new Uint8Array([1, 2, 3])], "big.webp", { type: "image/webp" });
    prepareAvatar.mockResolvedValueOnce(small);

    await user.upload(picker(), big);
    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(prepareAvatar).toHaveBeenCalledWith(big);
    expect(puts()[0]?.body).toBe(small);
  });

  it("ends the sending state and shows the network error when the upload cannot reach the server", async () => {
    const { user } = setup(atlas, {
      "PUT /intelligences/a1/avatar": () => {
        throw new TypeError("Failed to fetch");
      },
    });
    await user.upload(picker(), png());

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Couldn’t upload the photo. Try again.",
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(screen.getByAltText("Preview of the new photo")).toBeInTheDocument();
    expect(headerPhoto()).toBeNull();
  });

  it("refuses a file it cannot decode without leaving the photo sending", async () => {
    const { user, puts } = setup(atlas);
    prepareAvatar.mockRejectedValueOnce(new Error("could not decode"));

    await user.upload(picker(), png());
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "That image can’t be used. Use a PNG, JPEG or WebP of up to 2 MB.",
    );
    expect(screen.queryByRole("status")).toBeNull();
    expect(puts()).toHaveLength(0);
  });

  it("saves the text without touching the photo when the photo did not change", async () => {
    const old = { ...atlas, avatar_url: OLD_PHOTO };
    const { user, api } = setup(old, {
      "PATCH /intelligences/a1": (_url, init) =>
        json(200, { ...old, ...JSON.parse(String(init?.body)) }),
    });
    await user.type(screen.getByRole("textbox", { name: "Bio" }), " More.");

    await user.click(screen.getByRole("button", { name: "Save profile" }));
    await waitFor(() => expect(api.calls.filter((c) => c.method === "PATCH")).toHaveLength(1));
    expect(api.calls.some((c) => c.method === "PUT" || c.method === "DELETE")).toBe(false);
    expect(headerPhoto()).toHaveAttribute("src", OLD_PHOTO);
    expect(screen.queryByAltText("Preview of the new photo")).toBeNull();
    expect(toast.success).toHaveBeenCalledWith("Profile saved");
  });
});
