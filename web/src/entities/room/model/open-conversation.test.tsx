import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { useOpenConversation } from "./open-conversation";

/*
 * Which conversation the user is looking at (LAY-02): its page is open and the tab is visible.
 * Messages that arrive in it add no unread; in a hidden tab nothing is being read.
 */

const openRoom = () => useLiveStore.getState().openRoomId;

function setTabState(state: "visible" | "hidden") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

beforeEach(() => useLiveStore.setState({ openRoomId: null }));
afterEach(() => {
  // Drops the shadowing property so the prototype's getter answers again.
  delete (document as unknown as Record<string, unknown>).visibilityState;
});

describe("useOpenConversation", () => {
  it("opens the room while the page is mounted and clears it when the page goes", () => {
    const { unmount } = renderHook(() => useOpenConversation("r1"));
    expect(openRoom()).toBe("r1");
    unmount();
    expect(openRoom()).toBeNull();
  });

  it("follows the page from one conversation to the next", () => {
    const { rerender } = renderHook(({ id }) => useOpenConversation(id), {
      initialProps: { id: "r1" },
    });
    rerender({ id: "r2" });
    expect(openRoom()).toBe("r2");
  });

  it("is not open while the tab is hidden, and is again once it shows", () => {
    renderHook(() => useOpenConversation("r1"));
    setTabState("hidden");
    expect(openRoom()).toBeNull();
    setTabState("visible");
    expect(openRoom()).toBe("r1");
  });

  it("starts closed in a tab that is already hidden", () => {
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" });
    renderHook(() => useOpenConversation("r1"));
    expect(openRoom()).toBeNull();
  });
});
