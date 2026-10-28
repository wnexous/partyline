import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import type { Reaction } from "@/shared/api/types.gen";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ReactionChips } from "./reaction-chips";

expect.extend(matchers);

/*
 * The reactions under a message (RCT-01, RCT-08): one chip per emoji with its count, the viewer's own marked,
 * a tap that adds or takes back, and names a screen reader can use.
 */

const ME = "u1";
const reactions: Reaction[] = [
  { emoji: "👍", by: ["u2", ME] },
  { emoji: "🎉", by: ["u2"] },
  { emoji: "❤️", by: ["u2", "u3", "u4"] },
];

beforeEach(() => useLiveStore.setState({ locale: "en" }));

const renderChips = (onToggle?: (emoji: string) => void) =>
  renderWithProviders(<ReactionChips reactions={reactions} meId={ME} onToggle={onToggle} />);
const chips = (toggle: (emoji: string) => void = vi.fn()) => renderChips(toggle);
const staticChips = () => renderChips(undefined);

describe("the chips (RCT-01)", () => {
  it("are a list named Reactions with one chip for each emoji, in the order given", () => {
    chips();
    const list = screen.getByRole("list", { name: "Reactions" });
    const items = within(list).getAllByRole("listitem");
    expect(items).toHaveLength(3);
    expect(items.map((i) => i.textContent?.replace(/\s+/g, ""))).toEqual(["👍2", "🎉1", "❤️3"]);
  });

  it("show nothing for a message with no reactions", () => {
    renderWithProviders(<ReactionChips reactions={[]} meId={ME} onToggle={vi.fn()} />);
    expect(screen.queryByRole("list")).toBeNull();
  });

  it("are named with the emoji, the count and whether the viewer is in it (RCT-08)", () => {
    chips();
    expect(
      screen.getByRole("button", { name: "👍 2 reactions, including yours" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "🎉 1 reaction" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "❤️ 3 reactions" })).toBeInTheDocument();
  });

  it("say when the one reaction is the viewer's", () => {
    renderWithProviders(
      <ReactionChips reactions={[{ emoji: "👍", by: [ME] }]} meId={ME} onToggle={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: "👍 1 reaction, yours" })).toBeInTheDocument();
  });

  it("mark the viewer's own as pressed and with the tape fill, and no other", () => {
    chips();
    const mine = screen.getByRole("button", { name: /^👍/ });
    expect(mine).toHaveAttribute("aria-pressed", "true");
    expect(mine).toHaveClass("bg-tape");
    for (const name of [/^🎉/, /^❤️/]) {
      const other = screen.getByRole("button", { name });
      expect(other).toHaveAttribute("aria-pressed", "false");
      expect(other).not.toHaveClass("bg-tape");
    }
  });

  it("add the viewer's reaction on a tap, or take it back, by handing the emoji over", async () => {
    const toggle = vi.fn();
    chips(toggle);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /^🎉/ }));
    expect(toggle).toHaveBeenLastCalledWith("🎉");
    await user.click(screen.getByRole("button", { name: /^👍/ }));
    expect(toggle).toHaveBeenLastCalledWith("👍");
    expect(toggle).toHaveBeenCalledTimes(2);
  });

  it("answer the keyboard: Enter and Space press them", async () => {
    const toggle = vi.fn();
    chips(toggle);
    const user = userEvent.setup();
    await user.tab();
    expect(screen.getByRole("button", { name: /^👍/ })).toHaveFocus();
    await user.keyboard("{Enter}");
    await user.keyboard(" ");
    expect(toggle).toHaveBeenCalledTimes(2);
  });

  it("are as high as a finger needs on a touch device, and a size smaller with a mouse", () => {
    chips();
    const chip = screen.getByRole("button", { name: /^👍/ });
    expect(chip).toHaveClass("h-7");
    expect(chip).toHaveClass("pointer-coarse:h-9");
  });

  it("show the count in tabular figures, apart from the emoji", () => {
    chips();
    const chip = screen.getByRole("button", { name: /^❤️/ });
    expect(within(chip).getByText("3")).toHaveClass("tabular");
  });

  it("have no axe violations", async () => {
    const { container } = chips();
    expect(
      await axe(container, { rules: { "color-contrast": { enabled: false } } }),
    ).toHaveNoViolations();
  });
});

describe("for someone who may not react (RCT-08)", () => {
  it("are shown with their names and nothing to press", () => {
    staticChips();
    expect(screen.queryByRole("button")).toBeNull();
    const list = screen.getByRole("list", { name: "Reactions" });
    expect(within(list).getByText("👍 2 reactions, including yours")).toHaveClass("sr-only");
    expect(within(list).getByText("🎉 1 reaction")).toBeInTheDocument();
    expect(within(list).getByText("❤️ 3 reactions")).toBeInTheDocument();
  });

  it("still show the viewer's own with the tape fill", () => {
    staticChips();
    const list = screen.getByRole("list", { name: "Reactions" });
    const items = within(list).getAllByRole("listitem");
    expect(items[0]?.firstElementChild).toHaveClass("bg-tape");
    expect(items[1]?.firstElementChild).not.toHaveClass("bg-tape");
  });

  it("have no axe violations", async () => {
    const { container } = staticChips();
    expect(
      await axe(container, { rules: { "color-contrast": { enabled: false } } }),
    ).toHaveNoViolations();
  });
});

describe("in Portuguese", () => {
  beforeEach(() => useLiveStore.setState({ locale: "pt-BR" }));

  it("names them in the language of the app", () => {
    chips();
    expect(screen.getByRole("list", { name: "Reações" })).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "👍 2 reações, incluindo a sua" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "🎉 1 reação" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "❤️ 3 reações" })).toBeInTheDocument();
  });

  it("says when the one reaction is the viewer's", () => {
    renderWithProviders(
      <ReactionChips reactions={[{ emoji: "👍", by: [ME] }]} meId={ME} onToggle={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: "👍 1 reação, sua" })).toBeInTheDocument();
  });
});
