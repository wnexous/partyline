import type { ReactNode } from "react";

/**
 * A titled block of a settings page: the title and what it is for on the left (above, until the
 * content is wide enough for two columns: the sidebar leaves little room below 1024 px), the controls
 * on the right. `id` names the heading the section is labelled by.
 */
export function PageSection({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section aria-labelledby={id} className="grid gap-4 py-7 lg:grid-cols-[12rem_1fr] lg:gap-8">
      <div className="space-y-1">
        <h2 id={id} className="font-semibold tracking-[-0.01em]">
          {title}
        </h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  );
}
