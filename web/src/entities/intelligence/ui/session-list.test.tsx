import { screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import type { SessionInfo } from "@/shared/api/types.gen";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";
import { SessionList } from "./session-list";

const at = "2026-10-28T14:05:00Z";
const session = (label: string, over: Partial<SessionInfo> = {}): SessionInfo => ({
  label,
  harness: "claude",
  connected_at: at,
  ...over,
});

beforeEach(() => useLiveStore.setState({ locale: "en" }));

describe("SessionList (SESS-44)", () => {
  it("lists the principal and the labelled sessions on one line", () => {
    renderWithProviders(
      <SessionList
        sessions={[session(""), session("pi-2", { harness: "pi" })]}
        testId="sessions"
      />,
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
      <SessionList
        sessions={[session(""), session("wt", { outdated: true })]}
        online={false}
        testId="sessions"
      />,
    );
    expect(screen.queryByTestId("sessions")).toBeNull();
  });
});

describe("SessionList outdated clients (SESS-63)", () => {
  it("warns for an outdated session and names it", () => {
    renderWithProviders(
      <SessionList
        sessions={[session(""), session("claude-2", { outdated: true })]}
        testId="sessions"
      />,
    );
    const [warning] = screen.getAllByTestId("session-outdated");
    expect(warning).toHaveTextContent(
      ":claude-2 — Outdated client — run partyline update and reopen this session",
    );
    expect(warning).toHaveTextContent("partyline update");
    expect(screen.getAllByTestId("session-outdated")).toHaveLength(1);
  });

  it("shows the warning for an outdated principal even when it is the only session", () => {
    const { container } = renderWithProviders(
      <SessionList sessions={[session("", { outdated: true })]} testId="sessions" />,
    );
    expect(container).not.toBeEmptyDOMElement();
    expect(screen.getByTestId("session-outdated")).toHaveTextContent(
      "Outdated client — run partyline update and reopen this session",
    );
    expect(screen.queryByTestId("session-label")).toBeNull();
  });

  it("speaks Portuguese", () => {
    useLiveStore.setState({ locale: "pt-BR" });
    renderWithProviders(
      <SessionList sessions={[session("", { outdated: true })]} testId="sessions" />,
    );
    expect(screen.getByTestId("session-outdated")).toHaveTextContent(
      "Cliente desatualizado — rode partyline update e reabra esta sessão",
    );
  });
});
