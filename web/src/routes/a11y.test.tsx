import { screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeConnector,
  makeMe,
  makeMember,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { renderRoute } from "@/test/render";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * Automated accessibility checks (UI-24) on the shell, the room page and the login page, in both
 * locales. happy-dom has no layout or computed colours, so axe's colour-contrast rule cannot run
 * here; contrast is checked in the browser (Lighthouse, visual pass).
 */
const AXE_OPTIONS = { rules: { "color-contrast": { enabled: false } } };

stubLayout();
beforeEach(() => useLiveStore.setState({ locale: "en", presence: {}, connectors: {} }));
afterEach(() => vi.unstubAllGlobals());

function appApi(locale: "en" | "pt-BR") {
  return mockApi({
    "GET /me": json(200, makeMe({ role: "admin", locale })),
    "GET /rooms": json(200, { items: [makeRoom({ id: "r1", name: "deploys", last_seq: 3 })] }),
    "GET /rooms/r1": json(200, makeRoom({ id: "r1", name: "deploys", last_seq: 3 })),
    "GET /rooms/r1/members": json(200, {
      items: [
        makeMember({ id: "mb1", member_id: "a1", name: "release-bot", online: true }),
        makeMember({
          id: "mb2",
          kind: "chat",
          member_id: "c1",
          name: "Ops on call",
          online: undefined,
          owner: undefined,
          connector_kind: "telegram",
          chat_kind: "group",
        }),
      ],
    }),
    "GET /rooms/r1/messages": json(200, {
      items: [
        makeMessage({ seq: 3, author_kind: "agent", author_name: "release-bot", text: "**done**" }),
        makeMessage({
          seq: 2,
          author_kind: "chat",
          author_id: "c1",
          author_name: "Ops on call",
          sender_name: "Lia",
          text: "ship it",
        }),
        makeMessage({ seq: 1, text: "deploy 2.4" }),
      ],
      has_more: false,
    }),
    "GET /activity": json(200, {
      items: [
        {
          room: { id: "r1", name: "deploys" },
          message: makeMessage({ seq: 3, author_kind: "agent", author_name: "release-bot" }),
        },
      ],
    }),
    "GET /agents": json(200, { items: [makeAgent({ online: true })] }),
    "GET /connectors": json(200, {
      items: [makeConnector({ status: "error", last_error: "invalid token" })],
    }),
  });
}

describe.each(["en", "pt-BR"] as const)("accessibility (%s)", (locale) => {
  beforeEach(() => useLiveStore.setState({ locale }));

  it("the app shell with the dashboard has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/");
    await screen.findByRole("list", {
      name: locale === "en" ? "Latest messages" : "Últimas mensagens",
    });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the shell offers a skip link to the main content", async () => {
    appApi(locale);
    renderRoute("/rooms");
    const skip = await screen.findByRole("link", {
      name: locale === "en" ? "Skip to content" : "Pular para o conteúdo",
    });
    expect(skip).toHaveAttribute("href", "#main");
    expect(document.getElementById("main")?.tagName).toBe("MAIN");
  });

  it("the room page has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/rooms/r1");
    await screen.findByText("deploy 2.4");
    await screen.findByTestId("member-mb2");
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the login page has no axe violations", async () => {
    mockApi({ "GET /setup/status": json(200, { needs_setup: false }) });
    const { container } = renderRoute("/login");
    await screen.findByRole("button", { name: locale === "en" ? "Log in" : "Entrar" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
