import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CheckCheckIcon, CopyIcon, Trash2Icon } from "lucide-react";
import { afterEach, beforeEach, describe, expect, it, type Mock, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { ActionMenu, type ActionMenuProps, type MenuAction } from "@/components/action-menu";
import { AppProviders } from "@/components/app-providers";
import { useLiveStore } from "@/lib/live/store";
import { makeQueryClient } from "@/test/render";

expect.extend(matchers);

/*
 * The surface the actions of a row open into (ACT-01, ACT-02, ACT-06): a bottom sheet on a touch
 * device, a menu at the pointer with a mouse, the same actions in both.
 */

const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

let read: Mock<() => void>;
let copy: Mock<() => void>;
let remove: Mock<() => void>;
let close: Mock<() => void>;
let focusBack: Mock<(event: Event) => void>;

const actions = (): MenuAction[] => [
  { id: "read", label: "Mark as read", icon: CheckCheckIcon, onSelect: read },
  { id: "copy", label: "Copy text", icon: CopyIcon, onSelect: copy },
  { id: "delete", label: "Delete group", icon: Trash2Icon, onSelect: remove, destructive: true },
];

function renderMenu(over: Partial<ActionMenuProps> = {}) {
  const props: ActionMenuProps = {
    open: true,
    onClose: close,
    touch: true,
    title: "Deploys",
    description: "3 unread messages",
    at: { x: 120, y: 80 },
    actions: actions(),
    onCloseFocus: focusBack,
    ...over,
  };
  return render(
    <AppProviders queryClient={makeQueryClient()}>
      <ActionMenu {...props} />
    </AppProviders>,
  );
}

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  read = vi.fn();
  copy = vi.fn();
  remove = vi.fn();
  close = vi.fn();
  focusBack = vi.fn();
});
afterEach(() => vi.unstubAllGlobals());

describe("the sheet of a touch device (ACT-01, ACT-06)", () => {
  it("is a dialog named after what it acts on, with a line saying which one", async () => {
    renderMenu();
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    expect(sheet).toHaveAccessibleDescription("3 unread messages");
    expect(within(sheet).getByRole("heading", { name: "Deploys" })).toBeInTheDocument();
  });

  it("lists the actions as buttons, each tall enough for a finger", async () => {
    renderMenu();
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    for (const name of ["Mark as read", "Copy text", "Delete group"]) {
      const row = within(sheet).getByRole("button", { name });
      expect(row).toHaveClass("min-h-12");
    }
  });

  it("takes focus itself, on no action, so that nothing looks chosen; Tab reaches the first action", async () => {
    const user = userEvent.setup();
    renderMenu();
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    await waitFor(() => expect(sheet).toHaveFocus());
    expect(sheet).toHaveClass("outline-none");
    await user.tab();
    expect(screen.getByRole("button", { name: "Mark as read" })).toHaveFocus();
  });

  it("does something and closes when an action is chosen", async () => {
    const user = userEvent.setup();
    renderMenu();
    await user.click(await screen.findByRole("button", { name: "Copy text" }));
    expect(copy).toHaveBeenCalledTimes(1);
    expect(read).not.toHaveBeenCalled();
    expect(close).toHaveBeenCalledTimes(1);
  });

  describe("the finger that opened it", () => {
    // Some browsers send the release of a long press as a click on whatever is under the finger by
    // then, and by then that is a button of the sheet. No press began there, so it is not a tap.
    it("does not choose an action by a click that no press began on", async () => {
      renderMenu();
      const copyRow = await screen.findByRole("button", { name: "Copy text" });
      fireEvent.click(copyRow, { detail: 1 });
      expect(copy).not.toHaveBeenCalled();
      expect(close).not.toHaveBeenCalled();
    });

    it("does not take it for the close button's tap either", async () => {
      renderMenu();
      fireEvent.click(await screen.findByRole("button", { name: "Close" }), { detail: 1 });
      expect(close).not.toHaveBeenCalled();
    });

    it("does not let it reach the buttons of the header", async () => {
      const picked = vi.fn();
      renderMenu({
        header: (
          <button type="button" onClick={picked}>
            quick
          </button>
        ),
      });
      fireEvent.click(await screen.findByRole("button", { name: "quick" }), { detail: 1 });
      expect(picked).not.toHaveBeenCalled();
    });

    it("chooses on a tap, which begins with a press on the action", async () => {
      const user = userEvent.setup();
      renderMenu();
      const copyRow = await screen.findByRole("button", { name: "Copy text" });
      fireEvent.click(copyRow, { detail: 1 });
      expect(copy).not.toHaveBeenCalled();
      await user.click(copyRow);
      expect(copy).toHaveBeenCalledTimes(1);
      expect(close).toHaveBeenCalledTimes(1);
    });

    it("takes a click that is not a tap at all: the keyboard's, a screen reader's", async () => {
      renderMenu();
      fireEvent.click(await screen.findByRole("button", { name: "Copy text" }));
      expect(copy).toHaveBeenCalledTimes(1);
    });

    it("takes one tap for one click: a second stray click after it is not a tap", async () => {
      const user = userEvent.setup();
      renderMenu();
      const readRow = await screen.findByRole("button", { name: "Mark as read" });
      await user.click(readRow);
      expect(read).toHaveBeenCalledTimes(1);
      fireEvent.click(readRow, { detail: 1 });
      expect(read).toHaveBeenCalledTimes(1);
    });

    it("forgets a press that never became a click when it opens again", async () => {
      const user = userEvent.setup();
      const props = {
        open: true,
        onClose: close,
        touch: true,
        title: "Deploys",
        at: { x: 120, y: 80 },
        actions: actions(),
      } satisfies ActionMenuProps;
      const wrapper = (open: boolean) => (
        <AppProviders queryClient={makeQueryClient()}>
          <ActionMenu {...props} open={open} />
        </AppProviders>
      );
      const { rerender } = render(wrapper(true));
      const readRow = await screen.findByRole("button", { name: "Mark as read" });
      // A press that slid away before it lifted: no click follows, the press stays unspent.
      fireEvent.pointerDown(readRow);
      rerender(wrapper(false));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      rerender(wrapper(true));
      const again = await screen.findByRole("button", { name: "Mark as read" });
      fireEvent.click(again, { detail: 1 });
      expect(read).not.toHaveBeenCalled();
      await user.click(again);
      expect(read).toHaveBeenCalledTimes(1);
    });
  });

  it("sets the destructive action apart: its colour, its icon, its word, and a rule above it", async () => {
    renderMenu();
    const row = await screen.findByRole("button", { name: "Delete group" });
    expect(row).toHaveClass("text-destructive");
    expect(row.querySelector("svg")).not.toBeNull();
    expect(row.closest("li")).toHaveClass("border-t");
    // The ordinary ones are neither.
    const plain = screen.getByRole("button", { name: "Mark as read" });
    expect(plain).not.toHaveClass("text-destructive");
    expect(plain.closest("li")).not.toHaveClass("border-t");
  });

  it("has no rule when the only action is destructive", async () => {
    renderMenu({ actions: [actions()[2] as MenuAction] });
    const row = await screen.findByRole("button", { name: "Delete group" });
    expect(row.closest("li")).not.toHaveClass("border-t");
  });

  it("closes with Escape", async () => {
    const user = userEvent.setup();
    renderMenu();
    await screen.findByRole("dialog", { name: "Deploys" });
    await user.keyboard("{Escape}");
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("closes from its own close button, which is named", async () => {
    const user = userEvent.setup();
    renderMenu();
    await user.click(await screen.findByRole("button", { name: "Close" }));
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("keeps Tab inside it", async () => {
    const user = userEvent.setup();
    renderMenu();
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    for (let step = 0; step < 6; step += 1) {
      await user.tab();
      expect(sheet.contains(document.activeElement)).toBe(true);
    }
  });

  it("hands focus back to the caller when it goes", async () => {
    const { rerender } = renderMenu();
    await screen.findByRole("dialog", { name: "Deploys" });
    rerender(
      <AppProviders queryClient={makeQueryClient()}>
        <ActionMenu
          open={false}
          onClose={close}
          touch
          title="Deploys"
          at={{ x: 0, y: 0 }}
          actions={actions()}
          onCloseFocus={focusBack}
        />
      </AppProviders>,
    );
    await waitFor(() => expect(focusBack).toHaveBeenCalledTimes(1));
    expect(focusBack.mock.calls[0]?.[0]).toBeInstanceOf(Event);
  });

  it("closes in a different way when its title has no line under it: nothing is described", async () => {
    renderMenu({ description: undefined });
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    expect(sheet).not.toHaveAttribute("aria-describedby");
  });

  it("has no axe violations", async () => {
    renderMenu();
    const sheet = await screen.findByRole("dialog", { name: "Deploys" });
    expect(await axe(sheet, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("is named in Portuguese when the app is", async () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderMenu();
    expect(await screen.findByRole("button", { name: "Fechar" })).toBeInTheDocument();
  });
});

describe("the menu of a mouse (ACT-02, ACT-06)", () => {
  it("is a menu named after what it acts on, hanging from the pointer", async () => {
    renderMenu({ touch: false });
    const menu = await screen.findByRole("menu", { name: "Deploys" });
    expect(within(menu).getAllByRole("menuitem")).toHaveLength(3);
    const anchor = document.querySelector<HTMLElement>('[aria-haspopup="menu"]');
    expect(anchor?.style.left).toBe("120px");
    expect(anchor?.style.top).toBe("80px");
  });

  it("moves with the arrow keys and chooses with Enter", async () => {
    const user = userEvent.setup();
    renderMenu({ touch: false });
    const menu = await screen.findByRole("menu", { name: "Deploys" });
    await user.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitem", { name: "Mark as read" })).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(within(menu).getByRole("menuitem", { name: "Copy text" })).toHaveFocus();
    await user.keyboard("{ArrowUp}{ArrowUp}");
    // It wraps round to the last one.
    expect(within(menu).getByRole("menuitem", { name: "Delete group" })).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(remove).toHaveBeenCalledTimes(1);
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("closes with Escape", async () => {
    const user = userEvent.setup();
    renderMenu({ touch: false });
    await screen.findByRole("menu", { name: "Deploys" });
    await user.keyboard("{Escape}");
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("closes when the pointer goes down outside it", async () => {
    renderMenu({ touch: false });
    await screen.findByRole("menu", { name: "Deploys" });
    fireEvent.pointerDown(document.body, { pointerType: "mouse", button: 0 });
    await waitFor(() => expect(close).toHaveBeenCalled());
  });

  it("sets the destructive item apart too", async () => {
    renderMenu({ touch: false });
    const item = await screen.findByRole("menuitem", { name: "Delete group" });
    expect(item).toHaveAttribute("data-variant", "destructive");
    expect(screen.getByRole("menuitem", { name: "Mark as read" })).toHaveAttribute(
      "data-variant",
      "default",
    );
    expect(screen.getByRole("separator")).toBeInTheDocument();
  });

  it("does not give focus to its anchor when it closes without a place to put it", async () => {
    const { rerender } = renderMenu({ touch: false, onCloseFocus: undefined });
    await screen.findByRole("menu", { name: "Deploys" });
    const anchor = document.querySelector<HTMLElement>('[aria-haspopup="menu"]');
    rerender(
      <AppProviders queryClient={makeQueryClient()}>
        <ActionMenu
          open={false}
          onClose={close}
          touch={false}
          title="Deploys"
          at={{ x: 120, y: 80 }}
          actions={actions()}
        />
      </AppProviders>,
    );
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
    expect(document.activeElement).not.toBe(anchor);
  });

  it("has no axe violations", async () => {
    renderMenu({ touch: false });
    const menu = await screen.findByRole("menu", { name: "Deploys" });
    expect(await axe(menu, AXE_OPTIONS)).toHaveNoViolations();
  });
});

describe("what it holds above the actions", () => {
  it("shows it in the sheet and in the menu", async () => {
    const header = <p>quick reactions</p>;
    const { unmount } = renderMenu({ header });
    expect(await screen.findByText("quick reactions")).toBeInTheDocument();
    unmount();
    renderMenu({ header, touch: false });
    expect(await screen.findByText("quick reactions")).toBeInTheDocument();
  });
});
