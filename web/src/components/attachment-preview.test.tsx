import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  type AttachedFile,
  AttachmentPreview,
  AttachmentPreviews,
} from "@/components/attachment-preview";
import { useLiveStore } from "@/lib/live/store";
import { renderWithProviders } from "@/test/render";

/*
 * The composer's attachment previews (UI-07): an image shows a thumbnail made from an object URL
 * that is revoked with the card; every other file shows its kind icon with name and size. The row
 * scrolls sideways, so a long list never widens the composer.
 */

const createObjectURL = vi.fn(() => "blob:preview-1");
const revokeObjectURL = vi.fn();

beforeEach(() => {
  createObjectURL.mockClear();
  revokeObjectURL.mockClear();
  URL.createObjectURL = createObjectURL;
  URL.revokeObjectURL = revokeObjectURL;
});
afterEach(() => {
  createObjectURL.mockClear();
  revokeObjectURL.mockClear();
});

const fileOf = (name: string, type: string, size = 120 * 1024) =>
  new File([new Uint8Array(size)], name, { type });

const attach = (...files: File[]): AttachedFile[] => files.map((file, id) => ({ id, file }));

const png = () => fileOf("image.png", "image/png");

describe("an attachment preview (UI-07)", () => {
  it("shows an image's thumbnail, name and size, named as one preview", () => {
    const file = png();
    renderWithProviders(
      <ul>
        <AttachmentPreview file={file} onRemove={() => {}} />
      </ul>,
    );

    const preview = screen.getByRole("group", { name: "image.png, 120 KB" });
    expect(preview).toHaveTextContent("image.png");
    expect(preview).toHaveTextContent("120 KB");
    expect(preview.querySelector("img")).toHaveAttribute("src", "blob:preview-1");
    expect(createObjectURL).toHaveBeenCalledWith(file);
    expect(within(preview).getByRole("button", { name: "Remove image.png" })).toBeInTheDocument();
  });

  it("shows a non-image file's name and size, with an icon instead of an image", () => {
    renderWithProviders(
      <ul>
        <AttachmentPreview file={fileOf("notes.pdf", "application/pdf")} onRemove={() => {}} />
      </ul>,
    );

    const preview = screen.getByRole("group", { name: "notes.pdf, 120 KB" });
    expect(preview).toHaveTextContent("notes.pdf");
    expect(preview).toHaveTextContent("120 KB");
    expect(preview.querySelector("img")).toBeNull();
    expect(preview.querySelector("video, audio")).toBeNull();
    expect(createObjectURL).not.toHaveBeenCalled();
  });

  it.each([
    ["a video", "clip.mp4", "video/mp4"],
    ["an audio file", "note.m4a", "audio/mp4"],
  ])("shows an icon for %s and never plays it", (_what, name, type) => {
    renderWithProviders(
      <ul>
        <AttachmentPreview file={fileOf(name, type)} onRemove={() => {}} />
      </ul>,
    );

    const preview = screen.getByRole("group", { name: `${name}, 120 KB` });
    expect(preview.querySelector("video, audio")).toBeNull();
    expect(preview.querySelector("img")).toBeNull();
    expect(preview.querySelector("svg")).not.toBeNull();
  });

  it("reports the file to remove", async () => {
    const onRemove = vi.fn();
    const user = userEvent.setup();
    const file = png();
    renderWithProviders(
      <ul>
        <AttachmentPreview file={file} onRemove={onRemove} />
      </ul>,
    );

    await user.click(screen.getByRole("button", { name: "Remove image.png" }));
    expect(onRemove).toHaveBeenCalledTimes(1);
  });

  it("revokes the thumbnail's object URL when the preview leaves", () => {
    const { unmount } = renderWithProviders(
      <ul>
        <AttachmentPreview file={png()} onRemove={() => {}} />
      </ul>,
    );

    expect(revokeObjectURL).not.toHaveBeenCalled();
    unmount();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:preview-1");
  });
});

describe("the attachment row (UI-07)", () => {
  it("lists every file in order in a row that scrolls sideways", () => {
    const files = attach(png(), fileOf("build.log", "text/plain"));
    renderWithProviders(<AttachmentPreviews files={files} onRemove={() => {}} />);

    const list = screen.getByRole("list", { name: "Attachments" });
    expect(list).toHaveClass("overflow-x-auto");
    const previews = within(list).getAllByRole("group");
    expect(previews.map((preview) => preview.getAttribute("aria-label"))).toEqual([
      "image.png, 120 KB",
      "build.log, 120 KB",
    ]);
  });

  it("hands the chosen file back to the composer", async () => {
    const onRemove = vi.fn();
    const user = userEvent.setup();
    const file = fileOf("build.log", "text/plain");
    const files = attach(file);
    renderWithProviders(<AttachmentPreviews files={files} onRemove={onRemove} />);

    await user.click(screen.getByRole("button", { name: "Remove build.log" }));
    expect(onRemove).toHaveBeenCalledWith(files[0]?.id);
  });
});

describe("the preview in Portuguese", () => {
  it("names the row and the remove action in the app's language", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderWithProviders(<AttachmentPreviews files={attach(png())} onRemove={() => {}} />);

    expect(screen.getByRole("list", { name: "Anexos" })).toBeInTheDocument();
    expect(screen.getByRole("group", { name: "image.png, 120 KB" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remover image.png" })).toBeInTheDocument();
  });
});
