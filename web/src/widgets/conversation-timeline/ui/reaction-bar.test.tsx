import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { QUICK_REACTIONS } from "@/features/react-to-message";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ReactionBar, ReactionToolbar } from "./reaction-bar";

expect.extend(matchers);

/*
 * The six emoji at hand and the way to the rest (RCT-02): a bar in the action sheet of a touch device, and a
 * toolbar on the row for a mouse, which opens the picker from its last button.
 */

const AXE = { rules: { "color-contrast": { enabled: false } } };

beforeEach(() => useLiveStore.setState({ locale: "en" }));

const react = (emoji: string) => screen.getByRole("button", { name: `React with ${emoji}` });

describe("the bar of the sheet (RCT-02)", () => {
  function bar(over: Partial<{ reacted: string[]; expanded: boolean }> = {}) {
    const onReact = vi.fn();
    const onMore = vi.fn();
    const utils = renderWithProviders(
      <ReactionBar
        reacted={over.reacted ?? []}
        onReact={onReact}
        onMore={onMore}
        expanded={over.expanded ?? false}
      />,
    );
    return { ...utils, onReact, onMore };
  }

  it("is a group of the six emoji, in order, and a button for more", () => {
    bar();
    const group = screen.getByRole("group", { name: "Quick reactions" });
    const names = within(group)
      .getAllByRole("button")
      .map((b) => b.getAttribute("aria-label"));
    expect(names).toEqual([...QUICK_REACTIONS.map((e) => `React with ${e}`), "More reactions"]);
  });

  it("hands the emoji over on a tap", async () => {
    const { onReact } = bar();
    await userEvent.click(react("😂"));
    expect(onReact).toHaveBeenCalledWith("😂");
    expect(onReact).toHaveBeenCalledTimes(1);
  });

  it("marks the emoji the viewer used as pressed, with the tape fill", () => {
    bar({ reacted: ["❤️"] });
    expect(react("❤️")).toHaveAttribute("aria-pressed", "true");
    expect(react("❤️")).toHaveClass("bg-tape");
    expect(react("👍")).toHaveAttribute("aria-pressed", "false");
    expect(react("👍")).not.toHaveClass("bg-tape");
  });

  it("asks for more on a tap, and says whether the rest is open", async () => {
    const closed = bar();
    const more = screen.getByRole("button", { name: "More reactions" });
    expect(more).toHaveAttribute("aria-expanded", "false");
    await userEvent.click(more);
    expect(closed.onMore).toHaveBeenCalledTimes(1);
    closed.unmount();
    bar({ expanded: true });
    expect(screen.getByRole("button", { name: "More reactions" })).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("gives every button the size of a finger", () => {
    bar();
    for (const b of within(screen.getByRole("group")).getAllByRole("button")) {
      expect(b).toHaveClass("h-11", "max-w-12", "flex-1");
    }
  });

  it("is in Portuguese when the app is", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    bar();
    expect(screen.getByRole("group", { name: "Reações rápidas" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Reagir com 👍" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Mais reações" })).toBeInTheDocument();
  });

  it("has no axe violations", async () => {
    const { container } = bar({ reacted: ["👍"] });
    expect(await axe(container, AXE)).toHaveNoViolations();
  });
});

describe("the toolbar of a row (RCT-02)", () => {
  function toolbar(reacted: string[] = []) {
    const onReact = vi.fn();
    const utils = renderWithProviders(<ReactionToolbar reacted={reacted} onReact={onReact} />);
    return { ...utils, onReact };
  }
  const more = () => screen.getByRole("button", { name: "More reactions" });

  it("is a toolbar named Quick reactions with the six emoji and a button for more", () => {
    toolbar();
    const bar = screen.getByRole("toolbar", { name: "Quick reactions" });
    expect(
      within(bar)
        .getAllByRole("button")
        .map((b) => b.getAttribute("aria-label")),
    ).toEqual([...QUICK_REACTIONS.map((e) => `React with ${e}`), "More reactions"]);
  });

  it("is one stop of the Tab key and moves with the arrows", async () => {
    toolbar();
    const user = userEvent.setup();
    await user.tab();
    expect(react("👍")).toHaveFocus();
    await user.keyboard("{ArrowRight}");
    expect(react("❤️")).toHaveFocus();
    await user.keyboard("{End}");
    expect(more()).toHaveFocus();
    await user.tab();
    expect(document.body).toHaveFocus();
  });

  it("hands the emoji over on a tap or a key", async () => {
    const { onReact } = toolbar();
    const user = userEvent.setup();
    await user.click(react("🙏"));
    expect(onReact).toHaveBeenLastCalledWith("🙏");
    react("😮").focus();
    await user.keyboard("{Enter}");
    expect(onReact).toHaveBeenLastCalledWith("😮");
  });

  it("marks the emoji the viewer used as pressed", () => {
    toolbar(["😢"]);
    expect(react("😢")).toHaveAttribute("aria-pressed", "true");
    expect(react("😢")).toHaveClass("bg-tape");
    expect(react("🙏")).toHaveAttribute("aria-pressed", "false");
  });

  it("opens the picker from its last button and picks from it", async () => {
    const { onReact } = toolbar(["🔥"]);
    const user = userEvent.setup();
    expect(more()).toHaveAttribute("aria-expanded", "false");
    await user.click(more());
    const picker = await screen.findByRole("dialog", { name: "Pick a reaction" });
    expect(more()).toHaveAttribute("aria-expanded", "true");
    const grid = within(picker).getByRole("group", { name: "More reactions" });
    // The first the viewer used has the focus.
    await waitFor(() =>
      expect(within(grid).getByRole("button", { name: "React with 🔥" })).toHaveFocus(),
    );
    await user.click(within(grid).getByRole("button", { name: "React with 🎯" }));
    expect(onReact).toHaveBeenCalledWith("🎯");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  });

  it("closes the picker with Escape and gives focus back to the button", async () => {
    const { onReact } = toolbar();
    const user = userEvent.setup();
    await user.click(more());
    await screen.findByRole("dialog", { name: "Pick a reaction" });
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(onReact).not.toHaveBeenCalled();
    await waitFor(() => expect(more()).toHaveFocus());
  });

  it("has no axe violations, shut or with the picker open", async () => {
    const { container } = toolbar(["👍"]);
    expect(await axe(container, AXE)).toHaveNoViolations();
    await userEvent.click(more());
    const picker = await screen.findByRole("dialog");
    expect(await axe(picker, AXE)).toHaveNoViolations();
  });
});
