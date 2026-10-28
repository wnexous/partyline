import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { PrincipalOfflineLine } from "./principal-offline-line";

const since = "2026-10-28T14:05:00Z";

beforeEach(() => {
  useLiveStore.setState({ locale: "en" });
  vi.useFakeTimers({ toFake: ["Date"] });
  vi.setSystemTime(new Date("2026-10-28T14:10:00Z"));
});

describe("PrincipalOfflineLine (SESS-61)", () => {
  it("says since when the principal session has been away", () => {
    renderWithProviders(<PrincipalOfflineLine since={since} testId="line" />);
    expect(screen.getByTestId("line")).toHaveTextContent("Principal offline since 5 min. ago");
    expect(screen.getByText(/Principal offline/).closest("time")).toHaveAttribute(
      "datetime",
      since,
    );
  });

  it("speaks Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderWithProviders(<PrincipalOfflineLine since={since} testId="line" />);
    expect(screen.getByTestId("line")).toHaveTextContent(/Principal offline desde/);
  });

  it("shows nothing without a moment (the principal is live) or for an offline AI", () => {
    const { container, rerender } = renderWithProviders(
      <PrincipalOfflineLine since={undefined} testId="line" />,
    );
    expect(container).toBeEmptyDOMElement();
    rerender(<PrincipalOfflineLine since={since} online={false} testId="line" />);
    expect(screen.queryByTestId("line")).toBeNull();
  });
});
