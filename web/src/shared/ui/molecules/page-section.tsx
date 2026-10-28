import { type ReactNode, useId } from "react";

/*
 * A titled block of a page. Three layouts, one shell:
 * - `split` (the default, settings pages): the title and what it is for on the left (above, until
 *   the content is wide enough for two columns: the sidebar leaves little room below 1024 px), the
 *   controls on the right;
 * - `stacked`: the title with its actions on one row, the content under it;
 * - `list` (a list page's groups): a title and description over the rows, with the rows full width.
 * `id` names the heading the section is labelled by; without one, the section makes its own.
 */
export function PageSection({
  id,
  title,
  description,
  actions,
  layout = "split",
  headingClassName,
  children,
}: {
  id?: string;
  title: ReactNode;
  description?: ReactNode;
  /** The stacked layout's controls, at the end of the title's row. */
  actions?: ReactNode;
  layout?: "split" | "stacked" | "list";
  /** The stacked layout's heading classes, when they are not the default title style. */
  headingClassName?: string;
  children: ReactNode;
}) {
  const generated = useId();
  const headingId = id ?? generated;

  if (layout === "stacked") {
    return (
      <section aria-labelledby={headingId} className="space-y-3">
        <div className="flex items-center justify-between gap-3">
          <h2
            id={headingId}
            className={headingClassName ?? "text-lg font-semibold tracking-[-0.015em]"}
          >
            {title}
          </h2>
          {actions}
        </div>
        {children}
      </section>
    );
  }

  if (layout === "list") {
    return (
      <section aria-labelledby={headingId} className="pt-7 first:pt-5">
        <div className="px-4 pb-2 md:px-8">
          <h2 id={headingId} className="text-lg font-semibold tracking-[-0.015em]">
            {title}
          </h2>
          {description ? (
            <p className="mt-0.5 max-w-[65ch] text-sm text-pretty text-muted-foreground">
              {description}
            </p>
          ) : null}
        </div>
        {children}
      </section>
    );
  }

  return (
    <section
      aria-labelledby={headingId}
      className="grid gap-4 py-7 lg:grid-cols-[12rem_1fr] lg:gap-8"
    >
      <div className="space-y-1">
        <h2 id={headingId} className="font-semibold tracking-[-0.01em]">
          {title}
        </h2>
        {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  );
}
