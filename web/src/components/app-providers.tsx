import { type QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode, useEffect } from "react";
import { applyTheme } from "@/lib/theme";
import { I18nProvider } from "@/shared/i18n";
import { usePrefsStore } from "@/shared/lib/prefs-store";
import { TooltipProvider } from "@/shared/ui/primitives/tooltip";

/** Applies the theme preference and follows the system scheme while it is "system" (UI-23). */
function useThemeSync() {
  const theme = usePrefsStore((s) => s.theme);
  useEffect(() => {
    applyTheme(theme);
    if (theme !== "system" || typeof window.matchMedia !== "function") return;
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = () => applyTheme("system");
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, [theme]);
}

/** Providers shared by the app entry and the tests: query cache, locale, theme, tooltips. */
export function AppProviders({
  queryClient,
  children,
}: {
  queryClient: QueryClient;
  children: ReactNode;
}) {
  const locale = usePrefsStore((s) => s.locale);
  useThemeSync();
  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);
  return (
    <QueryClientProvider client={queryClient}>
      <I18nProvider locale={locale}>
        <TooltipProvider delayDuration={300}>{children}</TooltipProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}
