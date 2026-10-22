import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeConnector,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMessage,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";
import { sampleContacts } from "@/test/sample-contacts";

expect.extend(matchers);

vi.mock("@/lib/live", () => ({ startLive: vi.fn(() => () => {}) }));

/*
 * Automated accessibility checks (UI-24) on the messenger shell (home, phone home, account menu),
 * the room page and the login page, in both locales. happy-dom has no layout or computed colours, so axe's colour-contrast rule cannot run
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
    "GET /rooms/r1/attachments": json(200, {
      items: [
        {
          id: "f2",
          name: "rollout.png",
          mime: "image/png",
          size: 2048,
          status: "stored",
          kind: "image",
          message_id: "m-r1-3",
          message_seq: 3,
          author_kind: "agent",
          author_name: "release-bot",
          created_at: "2026-10-19T12:00:00Z",
        },
        {
          id: "f1",
          name: "call.mp4",
          mime: "video/mp4",
          size: 0,
          status: "too_large",
          kind: "video",
          message_id: "m-r1-2",
          message_seq: 2,
          author_kind: "chat",
          author_name: "Ops on call",
          created_at: "2026-10-19T11:00:00Z",
        },
      ],
      has_more: true,
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
    "GET /conversations": json(200, {
      items: [
        makeConversation({
          id: "r1",
          title: "deploys",
          unread: 2,
          last_message: {
            seq: 3,
            author_id: "a1",
            author_name: "release-bot",
            text: "done",
            created_at: "2026-10-19T12:00:00Z",
          },
        }),
        makeConversation({
          id: "r2",
          kind: "dm",
          title: "Atlas",
          counterpart: makeIntelligence({
            id: "a2",
            kind: "ai",
            role: "ai",
            display_name: "Atlas",
            handle: "atlas",
          }),
        }),
      ],
    }),
    "GET /rooms/d1": json(
      200,
      makeRoom({
        id: "d1",
        kind: "dm",
        name: "",
        my_level: "post",
        member_count: 2,
        participants: [
          makeMe().intelligence,
          makeIntelligence({
            id: "a2",
            kind: "ai",
            role: "ai",
            display_name: "Atlas",
            handle: "atlas",
          }),
        ],
      }),
    ),
    "GET /rooms/d1/members": json(200, { items: [] }),
    "GET /rooms/d1/messages": json(200, {
      items: [
        makeMessage({
          room_id: "d1",
          seq: 1,
          author_kind: "agent",
          author_id: "a2",
          author_name: "Atlas",
          author: makeIntelligence({
            id: "a2",
            kind: "ai",
            role: "ai",
            display_name: "Atlas",
            handle: "atlas",
          }),
          text: "Staging is on 2.4.1.",
        }),
      ],
      has_more: false,
    }),
    "GET /intelligences/a2": json(200, {
      ...makeIntelligence({
        id: "a2",
        kind: "ai",
        role: "ai",
        display_name: "Atlas",
        handle: "atlas",
      }),
      bio: "",
      can_edit: false,
      apps: [{ kind: "claude", label: "Claude Code" }],
      conversations: [],
      ai: {
        owner: makeMe().intelligence,
        machine: "devbox",
        machine_label: "devbox",
        folder: "/home/dev/atlas",
        folder_label: "~/atlas",
        app: "claude",
        online: true,
      },
    }),
    "PUT /rooms/d1/read": json(200, { room_id: "d1", seq: 1, unread: 0 }),
    "GET /agents": json(200, { items: [makeAgent({ online: true })] }),
    "GET /contacts": json(200, sampleContacts),
    "GET /connectors": json(200, {
      items: [makeConnector({ status: "error", last_error: "invalid token" })],
    }),
  });
}

describe.each(["en", "pt-BR"] as const)("accessibility (%s)", (locale) => {
  beforeEach(() => useLiveStore.setState({ locale }));

  const conversations = locale === "en" ? "Conversations" : "Conversas";

  it("the home page, with the conversation list beside the empty conversation, has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/");
    await screen.findByRole("list", { name: conversations });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the home page on a phone, where the list is the page, has no axe violations", async () => {
    stubViewport(360);
    appApi(locale);
    const { container } = renderRoute("/");
    await screen.findByRole("list", { name: conversations });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("a full-screen page on a phone, with its back bar, has no axe violations", async () => {
    stubViewport(360);
    appApi(locale);
    const { container } = renderRoute("/connectors");
    await screen.findByRole("link", {
      name: locale === "en" ? "Back to conversations" : "Voltar para as conversas",
    });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the open account menu has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "Account menu" : "Menu da conta",
      }),
    );
    const menu = await screen.findByRole("menu");
    expect(await axe(menu, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the new conversation dialog has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "New conversation" : "Nova conversa",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByTestId("contact-a1");
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the new group dialog has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      await screen.findByRole("button", { name: locale === "en" ? "New group" : "Novo grupo" }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("checkbox", { name: "Atlas" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
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

  it("a direct message page, with its header, participants and authors, has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/rooms/d1");
    await screen.findByText("Staging is on 2.4.1.");
    await screen.findByTestId("participant-a2");
    await screen.findAllByText("Claude Code");
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the room files tab has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    const { container } = renderRoute("/rooms/r1");
    await screen.findByText("deploy 2.4");
    await user.click(screen.getByRole("button", { name: locale === "en" ? "Files" : "Arquivos" }));
    await screen.findByTestId("file-f1");
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the machine approval page has no axe violations, before and after the code", async () => {
    const now = Date.now();
    mockApi({
      "GET /me": json(200, makeMe({ locale })),
      "POST /devices/lookup": json(200, {
        code: "K7P2QX9M",
        machine: "ana-laptop",
        ip: "203.0.113.24",
        status: "pending",
        created_at: new Date(now - 60_000).toISOString(),
        expires_at: new Date(now + 9 * 60_000).toISOString(),
      }),
    });
    const user = userEvent.setup();
    const { container } = renderRoute("/devices");
    const input = await screen.findByLabelText(
      locale === "en" ? "Code from your terminal" : "Código do seu terminal",
    );
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
    await user.type(input, "K7P2-QX9M");
    await user.click(
      screen.getByRole("button", { name: locale === "en" ? "Continue" : "Continuar" }),
    );
    await screen.findByRole("button", { name: locale === "en" ? "Approve" : "Aprovar" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the login page has no axe violations", async () => {
    mockApi({ "GET /setup/status": json(200, { needs_setup: false }) });
    const { container } = renderRoute("/login");
    await screen.findByRole("button", { name: locale === "en" ? "Log in" : "Entrar" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });
});
