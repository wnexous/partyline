import { act, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ConnectorStatusBadge } from "@/components/connector-status";
import { LiveIndicator } from "@/components/live-status";
import { StatusDot } from "@/components/status-dot";
import { useLiveStore } from "@/lib/live/store";
import { renderWithProviders } from "@/test/render";

/*
 * Presence has one colour: a quiet green for a live line (an online AI, a connected connection,
 * an open socket). Errors keep the muted red; everything else stays grey. The words come with
 * the dot every time, so the colour is never the only signal.
 */

beforeEach(() => useLiveStore.setState({ locale: "en" }));
afterEach(() => useLiveStore.setState({ locale: "en", status: "closed" }));

/** The decorative dot of a status line. */
const dot = (container: HTMLElement) => container.querySelector('[aria-hidden="true"]');

describe("StatusDot", () => {
  it("is a filled green dot when online and a hollow grey ring when offline", () => {
    const { container } = renderWithProviders(
      <>
        <StatusDot online withLabel />
        <StatusDot online={false} withLabel />
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

describe("LiveIndicator", () => {
  it("is a green dot while the socket is open, a green ring while it connects, grey when closed", () => {
    const { container } = renderWithProviders(<LiveIndicator />);
    act(() => useLiveStore.getState().setStatus("open"));
    expect(dot(container)?.className).toMatch(/\bbg-online\b/);
    act(() => useLiveStore.getState().setStatus("connecting"));
    expect(dot(container)?.className).toMatch(/\bborder-online\b/);
    expect(dot(container)?.className).not.toMatch(/\bbg-online\b/);
    act(() => useLiveStore.getState().setStatus("closed"));
    expect(dot(container)?.className).not.toMatch(/online/);
  });
});
