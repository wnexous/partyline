import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./confirm-dialog";

const base = {
  open: true,
  title: "Delete the group?",
  body: "It cannot be undone.",
  confirmLabel: "Delete group",
  onClose: () => {},
  onConfirm: () => {},
};

describe("ConfirmDialog", () => {
  it("names the question and labels the action in its own word, with Cancel translated", () => {
    render(<ConfirmDialog {...base} />);
    const dialog = screen.getByRole("alertdialog", { name: "Delete the group?" });
    expect(dialog).toHaveTextContent("It cannot be undone.");
    expect(screen.getByRole("button", { name: "Delete group" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("uses the given cancel label", () => {
    render(<ConfirmDialog {...base} cancelLabel="Keep it" />);
    expect(screen.getByRole("button", { name: "Keep it" })).toBeInTheDocument();
  });

  it("calls onConfirm without closing, and disables the action while pending", async () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();
    const { rerender } = render(
      <ConfirmDialog {...base} onConfirm={onConfirm} onClose={onClose} />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Delete group" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onClose).not.toHaveBeenCalled();

    rerender(<ConfirmDialog {...base} pending onConfirm={onConfirm} onClose={onClose} />);
    expect(screen.getByRole("button", { name: "Delete group" })).toBeDisabled();
  });

  it("calls onClose when Cancel closes it", async () => {
    const onClose = vi.fn();
    render(<ConfirmDialog {...base} onClose={onClose} />);
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("renders extra content between the question and the buttons", () => {
    render(
      <ConfirmDialog {...base}>
        <p role="alert">The last admin cannot leave.</p>
      </ConfirmDialog>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent("The last admin cannot leave.");
  });

  it("is not closed by a render with open false", () => {
    render(<ConfirmDialog {...base} open={false} />);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});
