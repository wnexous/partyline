import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { LiveIndicator } from "./live-status";

/*
 * Presence has one colour: a quiet green for a live line (an online AI, a connected connection,
 * an open socket). Errors keep the muted red; everything else stays grey. The words come with
 * the dot every time, so the colour is never the only signal.
 */

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => useLiveStore.setState({ locale: "en", status: "closed" }));

/** The decorative dot of a status line. */
const dot = (container: HTMLElement) => container.querySelector('[aria-hidden="true"]');

describe("LiveIndicator", () => {
  it("is a green dot while the socket is open, a green ring while it connects, grey when closed", () => {
    // The indicator is pure: the caller reads the store, the test drives the prop.
    const { container, rerender } = renderWithProviders(<LiveIndicator status="open" />);
    expect(dot(container)?.className).toMatch(/\bbg-online\b/);
    rerender(<LiveIndicator status="connecting" />);
    expect(dot(container)?.className).toMatch(/\bborder-online\b/);
    expect(dot(container)?.className).not.toMatch(/\bbg-online\b/);
    rerender(<LiveIndicator status="closed" />);
    expect(dot(container)?.className).not.toMatch(/online/);
  });
});
