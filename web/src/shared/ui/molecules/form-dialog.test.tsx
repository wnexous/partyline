import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { FormDialog } from "./form-dialog";

const base = {
  open: true,
  title: "Invite someone",
  description: "They get a link to join.",
  submitLabel: "Send invite",
  onOpenChange: () => {},
};

describe("FormDialog", () => {
  it("names the dialog by its title, with the description and the translated cancel", () => {
    render(
      <FormDialog {...base} onSubmit={() => {}}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    const dialog = screen.getByRole("dialog", { name: "Invite someone" });
    expect(dialog).toHaveTextContent("They get a link to join.");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Send invite" })).toBeEnabled();
  });

  it("submits the form it wraps around the fields", async () => {
    const onSubmit = vi.fn((e: React.FormEvent<HTMLFormElement>) => e.preventDefault());
    render(
      <FormDialog {...base} onSubmit={onSubmit}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Send invite" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("disables the submit button while pending, or while submitDisabled", () => {
    const { rerender } = render(
      <FormDialog {...base} pending onSubmit={() => {}}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    expect(screen.getByRole("button", { name: "Send invite" })).toBeDisabled();
    rerender(
      <FormDialog {...base} submitDisabled onSubmit={() => {}}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    expect(screen.getByRole("button", { name: "Send invite" })).toBeDisabled();
  });

  it("calls onOpenChange(false) from Cancel", async () => {
    const onOpenChange = vi.fn();
    render(
      <FormDialog {...base} onOpenChange={onOpenChange} onSubmit={() => {}}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("uses the given cancel label and names the form when asked", () => {
    render(
      <FormDialog {...base} cancelLabel="Keep" formLabel="Invite form" onSubmit={() => {}}>
        <input aria-label="Handle" />
      </FormDialog>,
    );
    expect(screen.getByRole("button", { name: "Keep" })).toBeInTheDocument();
    expect(screen.getByRole("form", { name: "Invite form" })).toBeInTheDocument();
  });
});
