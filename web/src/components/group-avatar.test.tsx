import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GroupAvatar } from "@/components/group-avatar";
import { avatarTone } from "@/components/intelligence/intelligence-avatar";
import { groupFace } from "@/lib/conversations";
import { useLiveStore } from "@/lib/live/store";
import { LoadedImage } from "@/test/image";
import { renderWithProviders } from "@/test/render";

/*
 * A group's face (GAV-07): the photo its managers gave it, or its initials on the tint of its id,
 * drawn as a rounded square where a person or an AI is a circle.
 */

const PHOTO = "/api/v1/rooms/r1/avatar?v=abc";

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => vi.unstubAllGlobals());

describe("groupFace", () => {
  it("is a summary with the name, no handle and the photo when there is one", () => {
    expect(groupFace("r1", "Launch")).toEqual({
      id: "r1",
      kind: "human",
      role: "user",
      display_name: "Launch",
      handle: "",
    });
    expect(groupFace("r1", "Launch", PHOTO).avatar_url).toBe(PHOTO);
    expect("avatar_url" in groupFace("r1", "Launch", "")).toBe(false);
  });
});

describe("GroupAvatar (GAV-07)", () => {
  it("shows the photo of the group", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { container } = renderWithProviders(
      <GroupAvatar group={{ id: "r1", name: "Launch", avatarUrl: PHOTO }} />,
    );
    expect(container.querySelector("img")).toHaveAttribute("src", PHOTO);
    expect(screen.queryByText("L")).toBeNull();
  });

  it("falls back to the initials of the name, on the tint of the id, without a photo", () => {
    renderWithProviders(<GroupAvatar group={{ id: "r1", name: "Launch crew" }} />);
    const face = screen.getByTestId("intelligence-avatar");
    expect(face).toHaveTextContent("LC");
    expect(face.querySelector('[data-slot="avatar-fallback"]')?.className).toContain(
      avatarTone("r1"),
    );
  });

  it("is a rounded square, the photo and the initials alike, where a person is a circle", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { rerender } = renderWithProviders(
      <GroupAvatar group={{ id: "r1", name: "Launch", avatarUrl: PHOTO }} />,
    );
    const root = () => screen.getByTestId("intelligence-avatar");
    // The frame and whatever it holds, the photo or the initials, are rounded the same way.
    expect(root().className).toContain("rounded-lg");
    expect(root().className).toContain("*:rounded-lg");
    expect(root().className).not.toContain("rounded-full");
    rerender(<GroupAvatar group={{ id: "r1", name: "Launch" }} />);
    expect(root().className).toContain("rounded-lg");
    expect(root().className).toContain("*:rounded-lg");
  });

  it("goes back to the initials when the photo is removed", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { container, rerender } = renderWithProviders(
      <GroupAvatar group={{ id: "r1", name: "Launch", avatarUrl: PHOTO }} />,
    );
    expect(container.querySelector("img")).not.toBeNull();
    rerender(<GroupAvatar group={{ id: "r1", name: "Launch" }} />);
    expect(container.querySelector("img")).toBeNull();
    expect(screen.getByTestId("intelligence-avatar")).toHaveTextContent("L");
  });

  it("is named by the group unless it sits next to the name", () => {
    vi.stubGlobal("Image", LoadedImage);
    const { rerender } = renderWithProviders(
      <GroupAvatar group={{ id: "r1", name: "Launch", avatarUrl: PHOTO }} />,
    );
    expect(screen.getByAltText("Launch")).toBeInTheDocument();
    rerender(<GroupAvatar group={{ id: "r1", name: "Launch", avatarUrl: PHOTO }} decorative />);
    expect(screen.getByTestId("intelligence-avatar")).toHaveAttribute("aria-hidden", "true");
    expect(screen.queryByAltText("Launch")).toBeNull();
  });
});
