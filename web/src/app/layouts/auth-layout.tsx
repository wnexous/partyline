import type { ReactNode } from "react";
import { LanguageSwitch } from "@/features/change-language";
import { ThemeToggle } from "@/features/toggle-theme";
import { Wordmark } from "@/shared/ui/atoms/brand";

/**
 * Public pages (setup, login, invite): one form on the paper, the page's purpose pasted above
 * the title on a strip of telegram tape.
 */
export function AuthLayout({
  tape,
  title,
  description,
  children,
  footer,
}: {
  tape: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div className="relative flex min-h-dvh flex-col bg-background">
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 bg-[repeating-linear-gradient(to_bottom,transparent_0,transparent_31px,color-mix(in_oklch,var(--border)_55%,transparent)_31px,color-mix(in_oklch,var(--border)_55%,transparent)_32px)] [mask-image:radial-gradient(ellipse_at_center,black_20%,transparent_75%)]"
      />
      <header className="relative flex items-center justify-between px-4 py-4 md:px-8">
        <Wordmark />
        <div className="flex items-center gap-1">
          <LanguageSwitch />
          <ThemeToggle />
        </div>
      </header>
      <main className="relative flex flex-1 items-start justify-center px-4 pt-6 pb-16 md:items-center md:pt-0">
        <div className="w-full max-w-[25rem]">
          <div className="rounded-xl border bg-card p-6 shadow-lg sm:p-8">
            <p className="mb-5 inline-block -rotate-1 rounded-[3px] bg-tape px-2.5 py-1 label-caps text-[0.7rem] text-tape-foreground shadow-sm">
              {tape}
            </p>
            <h1 className="text-2xl font-semibold tracking-[-0.02em] text-balance">{title}</h1>
            {description ? (
              <p className="mt-2 text-sm text-pretty text-muted-foreground">{description}</p>
            ) : null}
            <div className="mt-6">{children}</div>
          </div>
          {footer ? (
            <div className="mt-5 px-2 text-center text-sm text-pretty text-muted-foreground">
              {footer}
            </div>
          ) : null}
        </div>
      </main>
    </div>
  );
}
