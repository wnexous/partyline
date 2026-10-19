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
} as const;

export type MessageKey = keyof typeof en;
export type Dictionary = Record<MessageKey, string>;
