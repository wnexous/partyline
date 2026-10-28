import { create } from "zustand";
import { CURRENT_ORG_KEY } from "@/lib/orgs";
import {
  LOCALE_KEY,
  readStorage,
  readThemePreference,
  THEME_KEY,
  type ThemePreference,
  writeStorage,
} from "@/lib/theme";
import type { ConnectorStatus, Identity } from "@/shared/api/types.gen";
import { detectLocale, isLocale, type Locale } from "@/shared/i18n";

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
  /** Since when the socket has not been open (ms since epoch); null while open or idle. */
  offlineSince: number | null;
  presence: Record<string, Presence>;
  connectors: Record<string, ConnectorState>;
  /** The most recent identity.linked event, for the pairing dialog (UI-18). */
  lastLinkedIdentity: Identity | null;
  /**
   * The conversation the user is looking at: its page is open and the tab is visible. Messages
   * that arrive in it are read at once, so they add no unread (LAY-02).
   */
  openRoomId: string | null;
  theme: ThemePreference;
  locale: Locale;
  paletteOpen: boolean;
  /**
   * The organization the viewer picked in this browser (ORG-02); null until they pick one. What it
   * means is hooks/use-current-org.ts's to say: the pick of an organization they left counts for nothing.
   */
  currentOrgId: string | null;

  setStatus: (status: SocketStatus) => void;
  /** Restarts the offline grace after the page was hidden, unless the socket is open. */
  markVisible: () => void;
  setPresence: (agentId: string, presence: Presence) => void;
  setConnectorState: (connectorId: string, state: ConnectorState) => void;
  setLastLinkedIdentity: (identity: Identity | null) => void;
  setOpenRoom: (roomId: string | null) => void;
  setTheme: (theme: ThemePreference) => void;
  setLocale: (locale: Locale) => void;
  setPaletteOpen: (open: boolean) => void;
  setCurrentOrg: (orgId: string | null) => void;
  resetLive: () => void;
}

function initialLocale(): Locale {
  const stored = readStorage(LOCALE_KEY);
  if (isLocale(stored)) return stored;
  return detectLocale(typeof navigator === "undefined" ? [] : (navigator.languages ?? []));
}

export const useLiveStore = create<LiveState>()((set) => ({
  status: "closed",
  offlineSince: null,
  presence: {},
  connectors: {},
  lastLinkedIdentity: null,
  openRoomId: null,
  theme: readThemePreference(),
  locale: initialLocale(),
  paletteOpen: false,
  currentOrgId: readStorage(CURRENT_ORG_KEY),

  setStatus: (status) =>
    set((s) => {
      if (s.status === status) return s;
      if (status === "open") return { status, offlineSince: null };
      // Retries flip between connecting and closed; the outage started at the first drop.
      return { status, offlineSince: s.offlineSince ?? Date.now() };
    }),
  markVisible: () =>
    set((s) => {
      // The grace counts while the page is showing: a tab that comes back gets a fresh 2 s before
      // the banner (UI-06), since the outage timestamp it carries was set while nobody was looking.
      // An idle store (nothing started, no outage) has no grace to restart.
      if (s.status === "open" || s.offlineSince === null) return s;
      return { offlineSince: Date.now() };
    }),
  setPresence: (agentId, presence) =>
    set((s) => ({ presence: { ...s.presence, [agentId]: presence } })),
  setConnectorState: (connectorId, state) =>
    set((s) => ({ connectors: { ...s.connectors, [connectorId]: state } })),
  setLastLinkedIdentity: (identity) => set({ lastLinkedIdentity: identity }),
  setOpenRoom: (openRoomId) => set({ openRoomId }),
  setTheme: (theme) => {
    writeStorage(THEME_KEY, theme === "system" ? null : theme);
    set({ theme });
  },
  setLocale: (locale) => {
    writeStorage(LOCALE_KEY, locale);
    set({ locale });
  },
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
  setCurrentOrg: (currentOrgId) => {
    writeStorage(CURRENT_ORG_KEY, currentOrgId);
    set({ currentOrgId });
  },
  resetLive: () =>
    set({
      status: "closed",
      offlineSince: null,
      presence: {},
      connectors: {},
      lastLinkedIdentity: null,
      openRoomId: null,
    }),
}));
