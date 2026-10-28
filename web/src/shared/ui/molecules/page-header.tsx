import type { ReactNode } from "react";

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-4 border-b px-4 pt-6 pb-5 sm:flex-row sm:items-end sm:justify-between md:px-8 md:pt-8">
      <div className="min-w-0 space-y-1.5">
        <h1 className="text-2xl font-semibold tracking-[-0.02em] text-balance">{title}</h1>
        {description ? (
          <p className="max-w-[65ch] text-sm text-pretty text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
