import { create } from "zustand";
import type { ConnectorStatus, Identity } from "@/lib/api/types.gen";
import { detectLocale, isLocale, type Locale } from "@/lib/i18n";
import {
  LOCALE_KEY,
  readStorage,
  readThemePreference,
  THEME_KEY,
  type ThemePreference,
  writeStorage,
} from "@/lib/theme";

/*
 * Client-only state: the live connection, presence and connector status pushed by the socket,
 * and per-browser preferences. Server data lives in TanStack Query, never here.
 */

export type SocketStatus = "connecting" | "open" | "closed";

export interface Presence {
  online: boolean;
  last_seen_at?: string;
}

export interface ConnectorState {
  status: ConnectorStatus;
  last_error?: string;
}

export interface LiveState {
  status: SocketStatus;
  /** When `status` last changed (ms since epoch). */
  statusSince: number;
  presence: Record<string, Presence>;
  connectors: Record<string, ConnectorState>;
  /** The most recent identity.linked event, for the pairing dialog (UI-18). */
  lastLinkedIdentity: Identity | null;
  theme: ThemePreference;
  locale: Locale;
  paletteOpen: boolean;

  setStatus: (status: SocketStatus) => void;
  setPresence: (agentId: string, presence: Presence) => void;
  setConnectorState: (connectorId: string, state: ConnectorState) => void;
  setLastLinkedIdentity: (identity: Identity | null) => void;
  setTheme: (theme: ThemePreference) => void;
  setLocale: (locale: Locale) => void;
  setPaletteOpen: (open: boolean) => void;
  resetLive: () => void;
}

function initialLocale(): Locale {
  const stored = readStorage(LOCALE_KEY);
  if (isLocale(stored)) return stored;
  return detectLocale(typeof navigator === "undefined" ? [] : (navigator.languages ?? []));
}

export const useLiveStore = create<LiveState>()((set) => ({
  status: "closed",
  statusSince: Date.now(),
  presence: {},
  connectors: {},
  lastLinkedIdentity: null,
  theme: readThemePreference(),
  locale: initialLocale(),
  paletteOpen: false,

  setStatus: (status) =>
    set((s) => (s.status === status ? s : { status, statusSince: Date.now() })),
  setPresence: (agentId, presence) =>
    set((s) => ({ presence: { ...s.presence, [agentId]: presence } })),
  setConnectorState: (connectorId, state) =>
    set((s) => ({ connectors: { ...s.connectors, [connectorId]: state } })),
  setLastLinkedIdentity: (identity) => set({ lastLinkedIdentity: identity }),
  setTheme: (theme) => {
    writeStorage(THEME_KEY, theme === "system" ? null : theme);
    set({ theme });
  },
  setLocale: (locale) => {
    writeStorage(LOCALE_KEY, locale);
    set({ locale });
  },
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
  resetLive: () =>
    set({
      status: "closed",
      statusSince: Date.now(),
      presence: {},
      connectors: {},
      lastLinkedIdentity: null,
    }),
}));
