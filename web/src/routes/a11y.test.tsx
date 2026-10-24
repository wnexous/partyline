import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { axe } from "vitest-axe";
import * as matchers from "vitest-axe/matchers";
import { useLiveStore } from "@/lib/live/store";
import { json, mockApi } from "@/test/fetch";
import {
  makeAgent,
  makeAI,
  makeAIContact,
  makeConnector,
  makeConnectorKind,
  makeConversation,
  makeIntelligence,
  makeMe,
  makeMember,
  makeMembership,
  makeMessage,
  makeOrg,
  makePerson,
  makeProfile,
  makeRoom,
} from "@/test/fixtures";
import { stubLayout } from "@/test/layout";
import { stubViewport } from "@/test/media";
import { renderRoute } from "@/test/render";
import { lia, oldHelper, sampleContacts } from "@/test/sample-contacts";

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

// A profile of an AI its owner edits, and of a person a connector's user edits.
const aiProfile = makeProfile({
  ...makeAI(),
  bio: "Release captain.",
  can_edit: true,
  ai: {
    owner: makeIntelligence(),
    folder: "/home/dev/atlas",
    folder_label: "~/atlas",
    app: "claude",
    online: true,
    my_level: "manage",
  },
  apps: [
    { kind: "claude", label: "Claude Code" },
    { kind: "discord", label: "Discord: @atlasbot", connector_id: "k9" },
  ],
});
// The same AI as its manager sees it: in its home organization, where they may delete it, and as a
// guest of another one, where they may remove it; and its session may be ended.
const managedAIProfile = makeProfile({
  ...aiProfile,
  can_end_session: true,
  memberships: [
    makeMembership({ permission: "member", can_delete: true, delete_effect: "delete" }),
    makeMembership({
      org_id: "o2",
      org_name: "Globex",
      org_slug: "globex",
      permission: "guest",
      position_name: "Worktrees",
      can_delete: true,
      delete_effect: "remove",
    }),
  ],
});
const ownProfile = makeProfile({
  ...makeMe().intelligence,
  bio: "Release captain.",
  can_edit: true,
});
/** The viewer's organizations as `GET /orgs` lists them: they administer Acme and are a member of Globex. */
const ownOrgs = json(200, {
  items: [
    makeOrg({ permission: "admin" }),
    makeOrg({ id: "o2", name: "Globex", slug: "globex", permission: "member" }),
  ],
});

const personProfile = makeProfile({
  ...makePerson(),
  can_edit: true,
  person: {
    connector: { id: "k1", kind: "whatsapp", name: "ops-line" },
    external_id: "5511999990000",
    name_locked: true,
  },
  apps: [{ kind: "whatsapp", label: "WhatsApp: 5511999990000", connector_id: "k1" }],
  conversations: [
    { id: "r1", name: "support-bridge" },
    { id: "d9", name: "", kind: "dm" },
  ],
});

function appApi(locale: "en" | "pt-BR", extra: Parameters<typeof mockApi>[0] = {}) {
  return mockApi({
    "GET /me": json(200, makeMe({ role: "admin", locale })),
    "GET /intelligences/u1": json(200, ownProfile),
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
          author: makeAI({ id: "a9", display_name: "release-bot", handle: "release-bot" }),
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
          author: makePerson({ id: "p7", display_name: "Ops on call", handle: "ops-on-call" }),
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
        folder: "/home/dev/atlas",
        folder_label: "~/atlas",
        app: "claude",
        online: true,
      },
    }),
    "PUT /rooms/d1/read": json(200, { room_id: "d1", seq: 1, unread: 0 }),
    "GET /agents": json(200, { items: [makeAgent({ online: true })] }),
    "GET /contacts": json(200, sampleContacts),
    "GET /contacts/people": json(200, { items: [lia], has_more: false }),
    "GET /intelligences/a1": json(200, aiProfile),
    "GET /intelligences/p1": json(200, personProfile),
    "GET /connectors": json(200, {
      items: [makeConnector({ status: "error", last_error: "invalid token" })],
    }),
    ...extra,
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

  it("the new group dialog, with the organization it belongs to, has no axe violations", async () => {
    appApi(locale, { "GET /orgs": ownOrgs });
    const user = userEvent.setup();
    renderRoute("/");
    await user.click(
      await screen.findByRole("button", { name: locale === "en" ? "New group" : "Novo grupo" }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("combobox", {
      name: locale === "en" ? "Organization" : "Organização",
    });
    await within(dialog).findByRole("checkbox", { name: "Atlas" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the add connector dialog, with the organization it goes to, has no axe violations", async () => {
    appApi(locale, {
      "GET /orgs": ownOrgs,
      "GET /connector-kinds": json(200, { items: [makeConnectorKind()] }),
    });
    const user = userEvent.setup();
    renderRoute("/connectors");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "Add connector" : "Adicionar conector",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("combobox", {
      name: locale === "en" ? "Organization" : "Organização",
    });
    await within(dialog).findByRole("radio", { name: /Telegram/ });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the contacts page, with its organizations, the AIs it hosts and a connector's people, has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    const { container } = renderRoute("/contacts");
    await screen.findByRole("region", { name: "Acme" });
    await user.click(screen.getByRole("button", { name: /ops-line/ }));
    await screen.findByTestId("contact-p1");
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the settings page, with the profile editor and the appearance choices, has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/settings");
    await screen.findByRole("radio", {
      name: locale === "en" ? "Dark" : "Escuro",
    });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the settings page, with the default organization and the account deletion, has no axe violations", async () => {
    appApi(locale, {
      "GET /me": json(200, { ...makeMe({ locale }), default_org_id: "o2" }),
      "GET /orgs": ownOrgs,
    });
    const { container } = renderRoute("/settings");
    await screen.findByRole("combobox", {
      name:
        locale === "en" ? "Default organization for new AIs" : "Organização padrão para novas IAs",
    });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the dialog that deletes the account has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    renderRoute("/settings");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "Delete my account" : "Excluir minha conta",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByLabelText(locale === "en" ? "Password" : "Senha");
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the merge dialog of a hosted AI has no axe violations", async () => {
    // The old helper's home is Acme, where the user hosts Atlas and Notes too: it can be merged into either.
    appApi(locale, {
      "GET /contacts": json(200, {
        ...sampleContacts,
        hosted: [makeAIContact({ intelligence: oldHelper, folder: "", folder_label: "" })],
      }),
    });
    const user = userEvent.setup();
    renderRoute("/contacts");
    await user.click(
      await screen.findByRole("button", {
        name:
          locale === "en" ? "Merge Old helper into another AI" : "Mesclar Old helper em outra IA",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("combobox", { name: locale === "en" ? "AI" : "IA" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the contacts page on a phone has no axe violations", async () => {
    stubViewport(360);
    appApi(locale);
    const { container } = renderRoute("/contacts");
    await screen.findByRole("region", { name: "Acme" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("an AI's profile, with its editor, a photo being previewed, details and apps, has no axe violations", async () => {
    URL.createObjectURL = vi.fn(() => "blob:preview-1");
    URL.revokeObjectURL = vi.fn();
    appApi(locale);
    const user = userEvent.setup();
    const { container } = renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    await user.upload(
      screen.getByLabelText(locale === "en" ? "Upload photo" : "Enviar foto"),
      new File([new Uint8Array([137, 80, 78, 71])], "me.png", { type: "image/png" }),
    );
    await screen.findByAltText(
      locale === "en" ? "Preview of the new photo" : "Prévia da nova foto",
    );
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the merge dialog of an AI's profile has no axe violations", async () => {
    appApi(locale);
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "Merge Atlas into another AI" : "Mesclar Atlas em outra IA",
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await within(dialog).findByRole("combobox", { name: locale === "en" ? "AI" : "IA" });
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("an AI's profile, with its organizations and the actions its manager has, has no axe violations", async () => {
    appApi(locale, { "GET /intelligences/a1": json(200, managedAIProfile) });
    const { container } = renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
    await screen.findByRole("region", { name: locale === "en" ? "Organizations" : "Organizações" });
    expect(
      screen.getByRole("button", {
        name: locale === "en" ? "Remove from Globex" : "Remover de Globex",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: locale === "en" ? "End session" : "Encerrar sessão" }),
    ).toBeInTheDocument();
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the same profile on a phone has no axe violations", async () => {
    stubViewport(360);
    appApi(locale, { "GET /intelligences/a1": json(200, managedAIProfile) });
    const { container } = renderRoute("/contacts/a1");
    await screen.findByRole("region", { name: locale === "en" ? "Organizations" : "Organizações" });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the dialog that removes an identity from an organization has no axe violations", async () => {
    appApi(locale, { "GET /intelligences/a1": json(200, managedAIProfile) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "Remove from Globex" : "Remover de Globex",
      }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("the dialog that ends a session has no axe violations", async () => {
    appApi(locale, { "GET /intelligences/a1": json(200, managedAIProfile) });
    const user = userEvent.setup();
    renderRoute("/contacts/a1");
    await user.click(
      await screen.findByRole("button", {
        name: locale === "en" ? "End session" : "Encerrar sessão",
      }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(await axe(dialog, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("a person's profile, with its account and conversations, has no axe violations", async () => {
    appApi(locale);
    const { container } = renderRoute("/contacts/p1");
    await screen.findByRole("heading", { name: "Lia Costa", level: 1 });
    expect(await axe(container, AXE_OPTIONS)).toHaveNoViolations();
  });

  it("a profile on a phone has no axe violations", async () => {
    stubViewport(360);
    appApi(locale);
    const { container } = renderRoute("/contacts/a1");
    await screen.findByRole("heading", { name: "Atlas", level: 1 });
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
    await screen.findByTestId("member-mb1");
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
