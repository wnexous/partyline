import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLongPress } from "@/hooks/use-long-press";

/*
 * The press that opens a row's actions (ACT-01, ACT-02): a finger or a pen that rests for half a
 * second, nearly still. A tap, a scroll, a drag, a release that comes early and a mouse do not
 * open it; a right click, the context-menu key and a long click do, and keep the browser's own
 * menu closed.
 */

const opened: { x: number; y: number; element: HTMLElement | null }[] = [];
const clicked = vi.fn();

function Row({ enabled = true }: { enabled?: boolean }) {
  const press = useLongPress<HTMLAnchorElement>((where) => opened.push(where), { enabled });
  return (
    // biome-ignore lint/a11y/useValidAnchor: a stand-in for the row's link
    <a href="#row" data-testid="row" onClick={clicked} {...press}>
      row
    </a>
  );
}

const row = () => screen.getByTestId("row");
const finger = (over: Partial<PointerEventInit> = {}) => ({
  pointerType: "touch",
  pointerId: 1,
  isPrimary: true,
  button: 0,
  clientX: 100,
  clientY: 200,
  ...over,
});

beforeEach(() => {
  opened.length = 0;
  clicked.mockClear();
  vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "Date"] });
});
afterEach(() => vi.useRealTimers());

describe("a finger that rests (ACT-01)", () => {
  it("opens after half a second, where the finger is", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(499));
    expect(opened).toHaveLength(0);
    act(() => vi.advanceTimersByTime(1));
    expect(opened).toEqual([{ x: 100, y: 200, element: row() }]);
  });

  it("opens for a pen as it does for a finger", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger({ pointerType: "pen" }));
    act(() => vi.advanceTimersByTime(500));
    expect(opened).toHaveLength(1);
  });

  it("opens once per press, however long the finger stays", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(3_000));
    expect(opened).toHaveLength(1);
  });

  it("forgives a finger that shakes by up to ten pixels", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(200));
    fireEvent.pointerMove(row(), finger({ clientX: 100 + 10, clientY: 200 }));
    act(() => vi.advanceTimersByTime(500));
    expect(opened).toHaveLength(1);
  });
});

describe("what does not open it (ACT-01)", () => {
  it("a tap: released before the half second", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(400));
    fireEvent.pointerUp(row(), finger());
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a drag: the finger moved more than ten pixels", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    fireEvent.pointerMove(row(), finger({ clientY: 200 + 11 }));
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a drag that goes sideways is no press either", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    fireEvent.pointerMove(row(), finger({ clientX: 100 - 11 }));
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a scroll: the browser takes the pointer from the page", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(200));
    fireEvent.pointerCancel(row(), finger());
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a mouse: it has the right click and the button", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger({ pointerType: "mouse" }));
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a second finger: only the first one presses", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger({ isPrimary: false, pointerId: 2 }));
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a press while the row has no actions", () => {
    render(<Row enabled={false} />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("a row that goes away in the middle of a press", () => {
    const { unmount } = render(<Row />);
    fireEvent.pointerDown(row(), finger());
    unmount();
    act(() => vi.advanceTimersByTime(2_000));
    expect(opened).toHaveLength(0);
  });

  it("another finger's moves are not this press's moves", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    fireEvent.pointerMove(row(), finger({ pointerId: 9, clientY: 900 }));
    act(() => vi.advanceTimersByTime(500));
    expect(opened).toHaveLength(1);
  });
});

describe("the click that ends a long press (ACT-01)", () => {
  it("is not a click on the row: the conversation stays closed", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(row(), finger());
    fireEvent.click(row());
    expect(clicked).not.toHaveBeenCalled();
  });

  it("is swallowed once: the next tap on the row is a tap again", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(row(), finger());
    fireEvent.click(row());

    fireEvent.pointerDown(row(), finger());
    fireEvent.pointerUp(row(), finger());
    fireEvent.click(row());
    expect(clicked).toHaveBeenCalledTimes(1);
  });

  it("does not outlive a press that no click ever followed", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerCancel(row(), finger());
    // No click came. The next tap, a moment later, must still open the row.
    act(() => vi.advanceTimersByTime(1_000));
    fireEvent.click(row());
    expect(clicked).toHaveBeenCalledTimes(1);
  });

  it("leaves a plain tap alone", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    fireEvent.pointerUp(row(), finger());
    fireEvent.click(row());
    expect(clicked).toHaveBeenCalledTimes(1);
  });
});

describe("the context menu (ACT-02)", () => {
  it("a right click opens at the pointer and keeps the browser's own menu closed", () => {
    render(<Row />);
    const notPrevented = fireEvent.contextMenu(row(), { clientX: 310, clientY: 42 });
    expect(notPrevented).toBe(false);
    expect(opened).toEqual([{ x: 310, y: 42, element: row() }]);
  });

  it("the context-menu key or Shift+F10 comes with no position: it opens at the row", () => {
    render(<Row />);
    row().getBoundingClientRect = () =>
      ({ left: 20, top: 60, right: 220, bottom: 100, width: 200, height: 40 }) as DOMRect;
    fireEvent.contextMenu(row(), { clientX: 0, clientY: 0 });
    expect(opened).toEqual([{ x: 20, y: 100, element: row() }]);
  });

  it("a long click of a screen reader arrives as a context menu and opens too", () => {
    render(<Row />);
    fireEvent.contextMenu(row(), { clientX: 12, clientY: 34, pointerType: "touch" });
    expect(opened).toHaveLength(1);
  });

  it("opens once when the long press and the browser's own context menu both come", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    fireEvent.contextMenu(row(), { clientX: 100, clientY: 200 });
    expect(opened).toHaveLength(1);
  });

  it("opens again for a second right click that comes right after the first", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger({ pointerType: "mouse", button: 2 }));
    fireEvent.contextMenu(row(), { clientX: 10, clientY: 10 });
    act(() => vi.advanceTimersByTime(50));
    fireEvent.pointerDown(row(), finger({ pointerType: "mouse", button: 2 }));
    fireEvent.contextMenu(row(), { clientX: 20, clientY: 20 });
    expect(opened.map((o) => [o.x, o.y])).toEqual([
      [10, 10],
      [20, 20],
    ]);
  });

  it("opens for a right click soon after a finger's long press: a mouse is a new press", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    fireEvent.pointerUp(row(), finger());
    fireEvent.pointerDown(row(), finger({ pointerType: "mouse", button: 2, clientX: 5 }));
    fireEvent.contextMenu(row(), { clientX: 5, clientY: 6 });
    expect(opened).toHaveLength(2);
  });

  it("still keeps the browser's menu closed after the long press opened ours", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), finger());
    act(() => vi.advanceTimersByTime(500));
    expect(fireEvent.contextMenu(row(), { clientX: 100, clientY: 200 })).toBe(false);
  });

  it("leaves the browser's menu alone on a row with no actions", () => {
    render(<Row enabled={false} />);
    expect(fireEvent.contextMenu(row())).toBe(true);
    expect(opened).toHaveLength(0);
  });
});
