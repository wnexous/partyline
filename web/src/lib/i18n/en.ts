/**
 * English strings. This dictionary defines the key set: every other locale must provide exactly
 * these keys (enforced by the type of pt-BR.ts and by i18n.test.ts). Placeholders use {name}.
 */
export const en = {
  "app.name": "partyline",

  "common.loading": "Loading…",
  "common.retry": "Try again",
  "common.cancel": "Cancel",
  "common.save": "Save",
  "common.copy": "Copy",
  "common.copied": "Copied",
  "common.close": "Close",

  "validation.required": "Required",
  "validation.username": "3 to 32 characters: lowercase letters, digits, _ or -",
  "validation.passwordMin": "At least {min} characters",
  "validation.tooLong": "At most {max} characters",

  "error.network": "Can't reach the server. Check your connection and try again.",
  "error.generic": "Something went wrong. Try again.",
  "nav.label": "Main",
  "nav.overview": "Overview",
  "nav.rooms": "Rooms",
  "nav.agents": "Agents",
  "nav.connectors": "Connectors",
  "nav.identities": "Identities",
  "nav.tokens": "API tokens",
  "nav.settings": "Settings",
  "nav.admin": "Admin",
  "nav.adminUsers": "Users",
  "nav.groupWork": "Work",
  "nav.groupConnections": "Connections",
  "nav.groupAccount": "Account",
  "nav.toggleSidebar": "Toggle sidebar",

  "shell.userMenu": "Account menu",
  "shell.logout": "Log out",
  "shell.roleAdmin": "Admin",

  "theme.label": "Theme",
  "theme.system": "System",
  "theme.light": "Light",
  "theme.dark": "Dark",

  "locale.label": "Language",

  "live.open": "Live",
  "live.connecting": "Connecting",
  "live.closed": "Offline",
  "live.reconnectingTitle": "Connection lost. Reconnecting…",
  "live.reconnectingBody":
    "Live updates resume on their own; anything missed is fetched when the line is back.",

  "forbidden.title": "Admins only",
  "forbidden.body":
    "This area manages accounts and invites. Ask an instance admin if you need access.",
  "forbidden.back": "Back to overview",

  "notFound.title": "Nothing on this line",
  "notFound.body": "The page you opened doesn't exist or was moved.",
  "notFound.back": "Go to overview",

  "crash.title": "This screen hit an error",
  "crash.body": "Reload the page. If it keeps happening, the server log has the details.",

  "page.overviewTitle": "Overview",
  "page.overviewBody": "Who is on the line and what is moving through your rooms.",
  "page.roomsTitle": "Rooms",
  "page.roomsBody":
    "A room connects agents, chats and people. A message posted by one member reaches the others.",
  "page.agentsTitle": "Agents",
  "page.agentsBody": "Claude Code sessions that joined through partyline channel.",
  "page.connectorsTitle": "Connectors",
  "page.connectorsBody": "Your WhatsApp, Telegram, Discord and webhook accounts.",
  "page.identitiesTitle": "Identities",
  "page.identitiesBody": "Chat accounts verified as yours. Your agents trust messages from them.",
  "page.tokensTitle": "API tokens",
  "page.tokensBody": "Tokens for agents and scripts. A secret is shown once, when created.",
  "page.settingsTitle": "Settings",
  "page.settingsBody": "Profile, password, sessions, theme and language.",
  "page.adminUsersTitle": "Users",
  "page.adminUsersBody": "Accounts on this instance, their roles and status.",
  "page.emptyTitle": "Nothing here yet",
} as const;

export type MessageKey = keyof typeof en;
export type Dictionary = Record<MessageKey, string>;
