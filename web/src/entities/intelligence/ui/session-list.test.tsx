import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { SessionInfo } from "@/shared/api/types.gen";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { SessionList } from "./session-list";

const at = "2026-10-28T14:05:00Z";
const session = (label: string): SessionInfo => ({ label, harness: "claude", connected_at: at });

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("SessionList (SESS-44)", () => {
  it("lists the principal and the labelled sessions on one line", () => {
    renderWithProviders(
      <SessionList sessions={[session(""), session("pi-2")]} testId="sessions" />,
    );
    const line = screen.getByTestId("sessions");
    expect(line).toHaveTextContent(/principal\s*·\s*:pi-2/);
    expect(screen.getAllByTestId("session-label").map((el) => el.textContent)).toEqual([
      "principal",
      ":pi-2",
    ]);
  });

  it("shows nothing for a single principal session, for no session, or for an offline AI", () => {
    const { container, rerender } = renderWithProviders(
      <SessionList sessions={[session("")]} testId="sessions" />,
    );
    expect(container).toBeEmptyDOMElement();
    rerender(<SessionList sessions={undefined} testId="sessions" />);
    expect(screen.queryByTestId("sessions")).toBeNull();
    rerender(
      <SessionList sessions={[session(""), session("wt")]} online={false} testId="sessions" />,
    );
    expect(screen.queryByTestId("sessions")).toBeNull();
  });
});
