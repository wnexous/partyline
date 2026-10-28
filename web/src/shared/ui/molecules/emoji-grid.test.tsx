import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { I18nProvider } from "@/shared/i18n";
import { EmojiGrid } from "@/shared/ui/molecules/emoji-grid";

// The grid takes its emoji as props; the values below stand in for realistic picker input
// (the reaction constants stay in lib/reactions until the picker moves in a later task).
const MORE_REACTIONS = [
  "👍",
  "❤️",
  "😂",
  "😮",
  "😢",
  "🙏",
  "🔥",
  "🎯",
  "💡",
  "🚀",
  "🎉",
  "🌟",
  "🐛",
  "📌",
  "💯",
  "🎨",
  "⚽",
  "🍕",
  "🎁",
  "💎",
  "🌈",
  "🍀",
  "🔔",
  "🎵",
  "☕",
  "🌙",
  "⭐",
  "🎊",
  "💥",
  "👀",
  "🙌",
  "👏",
  "🤝",
  "💪",
  "🧠",
  "❓",
  "‼️",
  "💤",
  "👎",
  "👀",
  "🫡",
  "🥳",
  "😎",
  "🤔",
  "😴",
  "🥺",
  "😭",
  "😡",
] as const;
const PICKER_COLUMNS = 8;

import { render } from "@testing-library/react";

expect.extend(matchers);

/*
 * The larger set of emoji behind "More" (RCT-02): a grid of buttons that is one stop of the Tab key and moves
 * with the arrows, and tells which of them the viewer used already.
 */

function grid(
  over: Partial<{
    reacted: string[];
    onPick: (emoji: string) => void;
    size: "touch" | "pointer";
  }> = {},
) {
  const onPick = over.onPick ?? vi.fn();
  const utils = render(
    <I18nProvider locale="en">
      <EmojiGrid
        emojis={MORE_REACTIONS}
        columns={PICKER_COLUMNS}
        reacted={over.reacted ?? []}
        onPick={onPick}
        label="Pick a reaction"
        size={over.size ?? "pointer"}
      />
      ,
    </I18nProvider>,
  );
  return { ...utils, onPick };
}
const button = (emoji: string) => screen.getByRole("button", { name: `React with ${emoji}` });
/** The emoji at a place of the grid: 0 is the first, 8 the first of the second row. */
const at = (index: number) => button(MORE_REACTIONS[index] as string);

describe("the grid (RCT-02)", () => {
  it("is a group named for what it does, with a button for each emoji", () => {
    grid();
    const group = screen.getByRole("group", { name: "Pick a reaction" });
    expect(within(group).getAllByRole("button")).toHaveLength(MORE_REACTIONS.length);
    expect(at(0)).toBeInTheDocument();
    expect(button("🐛")).toBeInTheDocument();
  });

  it("lays them out in eight columns", () => {
    grid();
    expect(screen.getByRole("group").style.gridTemplateColumns).toBe("repeat(8, minmax(0, 1fr))");
  });

  it("hands the emoji over on a tap", async () => {
    const { onPick } = grid();
    await userEvent.click(button("🔥"));
    expect(onPick).toHaveBeenCalledWith("🔥");
    expect(onPick).toHaveBeenCalledTimes(1);
  });

  it("marks the emoji the viewer used as pressed, and no other", () => {
    grid({ reacted: ["🎉", "👎"] });
    expect(button("🎉")).toHaveAttribute("aria-pressed", "true");
    expect(button("👎")).toHaveAttribute("aria-pressed", "true");
    expect(button("🔥")).toHaveAttribute("aria-pressed", "false");
    expect(button("🎉")).toHaveClass("bg-tape");
    expect(button("🔥")).not.toHaveClass("bg-tape");
  });

  it("has no axe violations", async () => {
    const { container } = grid({ reacted: ["🔥"] });
    expect(
      await axe(container, { rules: { "color-contrast": { enabled: false } } }),
    ).toHaveNoViolations();
  });

  it("is large enough for a finger on a touch device", () => {
    grid({ size: "touch" });
    expect(at(0)).toHaveClass("h-11");
    expect(at(0)).toHaveClass("w-full", "max-w-11");
  });

  it("is a size smaller for a mouse", () => {
    grid({ size: "pointer" });
    expect(at(0)).toHaveClass("size-8");
  });
});

describe("the keyboard (RCT-08)", () => {
  const tabbable = () =>
    screen.getAllByRole("button").filter((b) => b.getAttribute("tabindex") === "0");

  it("is one stop of the Tab key: the first emoji, or the first the viewer used", () => {
    const first = grid();
    expect(tabbable()).toEqual([at(0)]);
    first.unmount();
    grid({ reacted: ["🔥", "🎉"] });
    expect(tabbable()).toEqual([button("🔥")]);
  });

  it("moves one emoji with Left and Right and a row with Up and Down", async () => {
    grid();
    const user = userEvent.setup();
    await user.tab();
    expect(at(0)).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(at(1)).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    // Eight emoji per row: the one under the second is the second of the next row.
    expect(at(9)).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(at(8)).toHaveFocus();
    await user.keyboard("{ArrowUp}");
    expect(at(0)).toHaveFocus();
  });

  it("stops at the edges instead of wrapping", async () => {
    grid();
    const user = userEvent.setup();
    await user.tab();
    await user.keyboard("{ArrowLeft}{ArrowUp}");
    expect(at(0)).toHaveFocus();
    await user.keyboard("{Control>}{End}{/Control}");
    const last = MORE_REACTIONS.length - 1;
    expect(at(last)).toHaveFocus();
    await user.keyboard("{ArrowRight}{ArrowDown}");
    expect(at(last)).toHaveFocus();
  });

  it("stays where it is when a Down would leave the grid, and does not slide to the last emoji", async () => {
    grid();
    const user = userEvent.setup();
    await user.click(at(40));
    await user.keyboard("{ArrowRight}{ArrowDown}");
    expect(at(41)).toHaveFocus();
  });

  it("goes to the ends of a row with Home and End, and of the grid with Control", async () => {
    grid();
    const user = userEvent.setup();
    await user.tab();
    await user.keyboard("{ArrowDown}{ArrowRight}{ArrowRight}");
    await user.keyboard("{End}");
    expect(at(15)).toHaveFocus();
    await user.keyboard("{Home}");
    expect(at(8)).toHaveFocus();
    await user.keyboard("{Control>}{Home}{/Control}");
    expect(at(0)).toHaveFocus();
  });

  it("keeps the stop where focus last was, so Tab out and back returns to it", async () => {
    grid();
    const user = userEvent.setup();
    await user.tab();
    await user.keyboard("{ArrowRight}{ArrowRight}");
    expect(tabbable()).toEqual([at(2)]);
  });

  it("picks with Enter and with Space", async () => {
    const { onPick } = grid();
    const user = userEvent.setup();
    await user.tab();
    await user.keyboard("{ArrowRight}{Enter}");
    expect(onPick).toHaveBeenLastCalledWith(MORE_REACTIONS[1]);
    await user.keyboard(" ");
    expect(onPick).toHaveBeenLastCalledWith(MORE_REACTIONS[1]);
    expect(onPick).toHaveBeenCalledTimes(2);
  });

  it("moves the stop to an emoji tapped, so the arrows go on from there", async () => {
    grid();
    const user = userEvent.setup();
    await user.click(button("🎯"));
    expect(tabbable()).toEqual([button("🎯")]);
    await user.keyboard("{ArrowRight}");
    expect(at(MORE_REACTIONS.indexOf("🎯") + 1)).toHaveFocus();
  });
});
