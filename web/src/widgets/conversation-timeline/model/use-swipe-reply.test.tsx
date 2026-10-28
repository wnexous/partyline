import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useSwipeReply } from "../model/use-swipe-reply";

/*
 * Dragging a message to the right to answer it (SWP-01..SWP-04): the row follows the finger, a
 * release past 60 px answers, anything shorter springs back, a mostly vertical drag is a scroll and
 * never a swipe, and nothing starts on a link, a button or the player.
 */

const answered = vi.fn();
const vibrate = vi.fn();
let reduced = false;

function Row({ enabled = true }: { enabled?: boolean }) {
  const swipe = useSwipeReply<HTMLElement>(answered, { enabled });
  return (
    <article data-testid="row" {...swipe}>
      <p data-testid="text">hello</p>
      <a href="#x" data-testid="link">
        the release notes
      </a>
      <button type="button" data-testid="button">
        a button
      </button>
      <input type="range" data-testid="slider" aria-label="Seek" readOnly />
      <div data-no-swipe data-testid="opt-out">
        opted out
      </div>
    </article>
  );
}

const row = () => screen.getByTestId("row");
const touch = (x: number, y: number, over: Partial<PointerEventInit> = {}) => ({
  pointerType: "touch",
  pointerId: 1,
  isPrimary: true,
  button: 0,
  clientX: x,
  clientY: y,
  ...over,
});
/** A finger that goes down at (100, 300) and drags by (dx, dy), one move per call. */
function drag(target: Element, dx: number, dy = 0, up = true) {
  fireEvent.pointerDown(target, touch(100, 300));
  fireEvent.pointerMove(target, touch(100 + dx, 300 + dy));
  if (up) fireEvent.pointerUp(target, touch(100 + dx, 300 + dy));
}

beforeEach(() => {
  vi.useFakeTimers({
    toFake: ["setTimeout", "clearTimeout", "Date", "requestAnimationFrame", "cancelAnimationFrame"],
  });
  answered.mockClear();
  vibrate.mockClear();
  reduced = false;
  vi.stubGlobal("navigator", { ...navigator, vibrate });
  vi.stubGlobal(
    "matchMedia",
    vi.fn((query: string) => ({
      matches: query.includes("prefers-reduced-motion") ? reduced : false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
    })),
  );
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const flush = () => act(() => vi.advanceTimersByTime(40));

describe("a drag to the right that goes far enough (SWP-01)", () => {
  it("answers when it is let go past 60 px", () => {
    render(<Row />);
    drag(row(), 80);
    expect(answered).toHaveBeenCalledTimes(1);
  });

  it("answers at exactly 60 px, and not at 59", () => {
    const { unmount } = render(<Row />);
    drag(row(), 60);
    expect(answered).toHaveBeenCalledTimes(1);
    unmount();
    answered.mockClear();
    render(<Row />);
    drag(row(), 59);
    expect(answered).not.toHaveBeenCalled();
  });

  it("answers once, however many times the finger moves", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    for (const x of [120, 150, 190, 230]) fireEvent.pointerMove(row(), touch(x, 302));
    fireEvent.pointerUp(row(), touch(230, 302));
    expect(answered).toHaveBeenCalledTimes(1);
  });

  it("follows the finger, with some resistance past the point where it answers", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(150, 300));
    flush();
    expect(row().style.transform).toBe("translateX(50px)");
    fireEvent.pointerMove(row(), touch(100 + 360, 300));
    flush();
    const far = Number(/translateX\((.+)px\)/.exec(row().style.transform)?.[1]);
    expect(far).toBeGreaterThan(60);
    expect(far).toBeLessThanOrEqual(96);
  });

  it("slows down past 60 px: a finger 100 px along has taken the row 74", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(200, 300));
    flush();
    expect(row().style.transform).toBe("translateX(74px)");
  });

  it("shows how far it has come, for the icon that appears as the row moves", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(130, 300));
    flush();
    expect(row().style.getPropertyValue("--swipe-progress")).toBe("0.5");
    fireEvent.pointerMove(row(), touch(220, 300));
    flush();
    expect(row().style.getPropertyValue("--swipe-progress")).toBe("1");
  });

  it("says when it will answer: marked while it is past 60 px and not before", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(150, 300));
    flush();
    expect(row()).not.toHaveAttribute("data-armed");
    fireEvent.pointerMove(row(), touch(170, 300));
    flush();
    expect(row()).toHaveAttribute("data-armed", "true");
    fireEvent.pointerMove(row(), touch(140, 300));
    flush();
    expect(row()).not.toHaveAttribute("data-armed");
  });

  it("a finger that goes back before it lets go answers nothing", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(200, 300));
    fireEvent.pointerMove(row(), touch(130, 300));
    fireEvent.pointerUp(row(), touch(130, 300));
    expect(answered).not.toHaveBeenCalled();
  });
});

describe("a light tick when it will answer (SWP-01)", () => {
  it("vibrates once as the finger crosses 60 px", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(140, 300));
    expect(vibrate).not.toHaveBeenCalled();
    fireEvent.pointerMove(row(), touch(165, 300));
    expect(vibrate).toHaveBeenCalledTimes(1);
    fireEvent.pointerMove(row(), touch(220, 300));
    fireEvent.pointerMove(row(), touch(240, 300));
    expect(vibrate).toHaveBeenCalledTimes(1);
  });

  it("ticks again when the finger comes back and crosses it again", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(170, 300));
    fireEvent.pointerMove(row(), touch(120, 300));
    fireEvent.pointerMove(row(), touch(175, 300));
    expect(vibrate).toHaveBeenCalledTimes(2);
  });

  it("is a short tick, not a buzz", () => {
    render(<Row />);
    drag(row(), 90);
    expect(vibrate).toHaveBeenCalledWith(10);
  });

  it("does nothing, and breaks nothing, where the browser cannot vibrate", () => {
    vi.stubGlobal("navigator", { ...navigator, vibrate: undefined });
    render(<Row />);
    drag(row(), 90);
    expect(answered).toHaveBeenCalledTimes(1);
  });

  it("does not tick for a drag that stops short", () => {
    render(<Row />);
    drag(row(), 40);
    expect(vibrate).not.toHaveBeenCalled();
  });
});

describe("what lets go of the row (SWP-02)", () => {
  it("springs back when it is let go before 60 px, and answers nothing", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(140, 300));
    flush();
    expect(row().style.transform).toBe("translateX(40px)");
    fireEvent.pointerUp(row(), touch(140, 300));
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
    expect(row().style.transition).toContain("transform");
    expect(row().style.getPropertyValue("--swipe-progress")).toBe("0");
  });

  it("springs back, without answering, when the system takes the finger", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(200, 300));
    fireEvent.pointerCancel(row(), touch(200, 300));
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
  });

  it("leaves no transition behind once it has come back", () => {
    render(<Row />);
    drag(row(), 40);
    act(() => vi.advanceTimersByTime(400));
    expect(row().style.transition).toBe("");
  });

  it("does not move the row for a drag that never started", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(104, 300));
    flush();
    expect(row().style.transform).toBe("");
  });

  it("the next drag starts from where the row rests, not from a row still coming back", () => {
    render(<Row />);
    drag(row(), 40);
    fireEvent.pointerDown(row(), touch(100, 300));
    expect(row().style.transition).toBe("none");
  });
});

describe("a drag that is not a swipe (SWP-02)", () => {
  it("is a scroll when it is mostly vertical: nothing moves and nothing answers", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(112, 360));
    fireEvent.pointerMove(row(), touch(190, 380));
    fireEvent.pointerUp(row(), touch(190, 380));
    flush();
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
  });

  it("is a scroll when the first move is as much up as across", () => {
    render(<Row />);
    drag(row(), 30, -30);
    expect(answered).not.toHaveBeenCalled();
  });

  it("never starts once it has been taken for a scroll, even if it later turns sideways", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(102, 340));
    fireEvent.pointerMove(row(), touch(260, 342));
    fireEvent.pointerUp(row(), touch(260, 342));
    expect(answered).not.toHaveBeenCalled();
  });

  it("is not a swipe to the left: the row does not move and the pointer is not taken", () => {
    render(<Row />);
    const capture = vi.fn();
    row().setPointerCapture = capture;
    drag(row(), -90);
    flush();
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
    expect(capture).not.toHaveBeenCalled();
  });

  it("never goes to the left of where it rests, however far back the finger comes", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(130, 300));
    flush();
    expect(row().style.transform).toBe("translateX(30px)");
    fireEvent.pointerMove(row(), touch(40, 300));
    flush();
    expect(row().style.transform).toBe("translateX(0px)");
    fireEvent.pointerUp(row(), touch(40, 300));
    expect(answered).not.toHaveBeenCalled();
  });

  it("is not a swipe when the finger rested first: that is a press", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    act(() => vi.advanceTimersByTime(600));
    fireEvent.pointerMove(row(), touch(190, 300));
    fireEvent.pointerUp(row(), touch(190, 300));
    expect(answered).not.toHaveBeenCalled();
  });

  it("is not a swipe when it is a tap", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerUp(row(), touch(100, 300));
    expect(answered).not.toHaveBeenCalled();
  });

  it("holds the pointer once it has started, so that the finger may leave the row", () => {
    render(<Row />);
    const capture = vi.fn();
    row().setPointerCapture = capture;
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(130, 300));
    expect(capture).toHaveBeenCalledWith(1);
  });

  it("does not hold the pointer for a scroll", () => {
    render(<Row />);
    const capture = vi.fn();
    row().setPointerCapture = capture;
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(100, 360));
    expect(capture).not.toHaveBeenCalled();
  });
});

describe("where it does not start (SWP-04)", () => {
  it.each([
    ["a link", "link"],
    ["a button", "button"],
    ["the seek bar", "slider"],
    ["whatever opts out", "opt-out"],
  ])("on %s", (_what, id) => {
    render(<Row />);
    drag(screen.getByTestId(id), 90);
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
  });

  it("on the text of the message it does", () => {
    render(<Row />);
    drag(screen.getByTestId("text"), 90);
    expect(answered).toHaveBeenCalledTimes(1);
  });

  it("with a mouse: it has the buttons and the menu", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300, { pointerType: "mouse" }));
    fireEvent.pointerMove(row(), touch(200, 300, { pointerType: "mouse" }));
    fireEvent.pointerUp(row(), touch(200, 300, { pointerType: "mouse" }));
    expect(answered).not.toHaveBeenCalled();
  });

  it("with a pen it does, as with a finger", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300, { pointerType: "pen" }));
    fireEvent.pointerMove(row(), touch(200, 300, { pointerType: "pen" }));
    fireEvent.pointerUp(row(), touch(200, 300, { pointerType: "pen" }));
    expect(answered).toHaveBeenCalledTimes(1);
  });

  it("where it is not enabled: nothing to answer, or no right to post", () => {
    render(<Row enabled={false} />);
    drag(row(), 90);
    expect(answered).not.toHaveBeenCalled();
    expect(row().style.transform).toBe("");
  });

  it("with a second finger: only the first one drags", () => {
    render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerDown(row(), touch(100, 300, { pointerId: 2, isPrimary: false }));
    fireEvent.pointerMove(row(), touch(190, 300, { pointerId: 2, isPrimary: false }));
    fireEvent.pointerUp(row(), touch(190, 300, { pointerId: 2, isPrimary: false }));
    expect(answered).not.toHaveBeenCalled();
  });
});

describe("reduced motion (SWP-03)", () => {
  it("does not animate the row back where the user prefers less motion", () => {
    reduced = true;
    render(<Row />);
    drag(row(), 40);
    expect(row().style.transform).toBe("");
    expect(row().style.transition).toBe("none");
  });

  it("answers just the same", () => {
    reduced = true;
    render(<Row />);
    drag(row(), 90);
    expect(answered).toHaveBeenCalledTimes(1);
  });
});

describe("tidying up", () => {
  it("lets a row that goes away in the middle of a swipe go", () => {
    const { unmount } = render(<Row />);
    fireEvent.pointerDown(row(), touch(100, 300));
    fireEvent.pointerMove(row(), touch(150, 300));
    unmount();
    expect(() => act(() => vi.advanceTimersByTime(1000))).not.toThrow();
    expect(answered).not.toHaveBeenCalled();
  });
});
