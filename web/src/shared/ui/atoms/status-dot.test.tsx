import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { StatusDot } from "./status-dot";

/*
 * Presence has one colour: a quiet green for a live line (an online AI, a connected connection,
 * an open socket). Errors keep the muted red; everything else stays grey. The words come with
 * the dot every time, so the colour is never the only signal.
 */

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => useLiveStore.setState({ locale: "en", status: "closed" }));

describe("StatusDot", () => {
  it("is a filled green dot when online and a hollow grey ring when offline", () => {
    const { container } = renderWithProviders(
      <>
        <StatusDot online label="Online" withLabel />
        <StatusDot online={false} label="Offline" withLabel />
      </>,
    );
    const [online, offline] = [...container.querySelectorAll('[aria-hidden="true"]')];
    expect(online?.className).toMatch(/\bbg-online\b/);
    expect(offline?.className).not.toMatch(/\bbg-online\b/);
    expect(offline?.className).toMatch(/\bborder-muted-foreground\b/);
    expect(screen.getByText("Online")).toBeInTheDocument();
    expect(screen.getByText("Offline")).toBeInTheDocument();
  });
});
