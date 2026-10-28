import { act } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useLiveStore } from "@/test/live-store";
import { renderWithProviders } from "@/test/render";

/** A controllable `prefers-color-scheme: dark` media query. */
function stubColorScheme(dark: boolean) {
  const listeners = new Set<(e: MediaQueryListEvent) => void>();
  const state = { dark };
  const matchMedia = vi.fn((query: string) => ({
    media: query,
    get matches() {
      return query.includes("prefers-color-scheme: dark") ? state.dark : false;
    },
    onchange: null,
    addEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => listeners.add(fn),
    removeEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => listeners.delete(fn),
    addListener: (fn: (e: MediaQueryListEvent) => void) => listeners.add(fn),
    removeListener: (fn: (e: MediaQueryListEvent) => void) => listeners.delete(fn),
    dispatchEvent: () => true,
  }));
  vi.stubGlobal("matchMedia", matchMedia);
  return {
    set(next: boolean) {
      state.dark = next;
      for (const fn of [...listeners]) fn({ matches: next } as MediaQueryListEvent);
    },
  };
}

const isDark = () => document.documentElement.classList.contains("dark");

describe("theme follows the system scheme (UI-23)", () => {
  beforeEach(() => {
    document.documentElement.classList.remove("dark");
    useLiveStore.setState({ locale: "en", theme: "system" });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("applies dark when the system prefers dark", () => {
    stubColorScheme(true);
    renderWithProviders(<p>x</p>);
    expect(isDark()).toBe(true);
    expect(document.documentElement.style.colorScheme).toBe("dark");
  });

  it("applies light when the system prefers light", () => {
    document.documentElement.classList.add("dark");
    stubColorScheme(false);
    renderWithProviders(<p>x</p>);
    expect(isDark()).toBe(false);
    expect(document.documentElement.style.colorScheme).toBe("light");
  });

  it("follows a live change of the system scheme", () => {
    const scheme = stubColorScheme(false);
    renderWithProviders(<p>x</p>);
    expect(isDark()).toBe(false);
    act(() => scheme.set(true));
    expect(isDark()).toBe(true);
    act(() => scheme.set(false));
    expect(isDark()).toBe(false);
  });

  it("keeps an explicit override over the system scheme", () => {
    const scheme = stubColorScheme(true);
    useLiveStore.setState({ theme: "light" });
    renderWithProviders(<p>x</p>);
    expect(isDark()).toBe(false);
    act(() => scheme.set(false));
    act(() => scheme.set(true));
    expect(isDark()).toBe(false);
  });
});
