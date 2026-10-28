import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { ConnectorStatusBadge } from "./connector-status";

/*
 * Presence has one colour: a quiet green for a live line (an online AI, a connected connection,
 * an open socket). Errors keep the muted red; everything else stays grey. The words come with
 * the dot every time, so the colour is never the only signal.
 */

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => useLiveStore.setState({ locale: "en", status: "closed" }));

describe("ConnectorStatusBadge", () => {
  it("shows connected as green, an error as the red, and the rest as grey", () => {
    const { container } = renderWithProviders(
      <>
        <ConnectorStatusBadge status="connected" />
        <ConnectorStatusBadge status="error" />
        <ConnectorStatusBadge status="connecting" />
        <ConnectorStatusBadge status="stopped" />
      </>,
    );
    const [connected, error, connecting, stopped] = [
      ...container.querySelectorAll('[aria-hidden="true"]'),
    ];
    expect(connected?.className).toMatch(/\bbg-online\b/);
    expect(error?.className).toMatch(/\bbg-destructive\b/);
    for (const quiet of [connecting, stopped]) {
      expect(quiet?.className).not.toMatch(/\bbg-online\b|\bbg-destructive\b/);
    }
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("Error")).toBeInTheDocument();
  });
});
