import { CheckIcon, CopyIcon } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * Copyable data: commands, configs, secrets and links. Values render in JetBrains Mono (data,
 * never decoration); the copy button confirms in place for two seconds.
 */

export function CopyButton({
  value,
  label,
  className,
}: {
  value: string;
  /** What is copied, for the button's accessible name ("Copy {label}"). */
  label: string;
  className?: string;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 2000);
    return () => window.clearTimeout(timer);
  }, [copied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  };

  return (
    <Button
      type="button"
      variant="ghost"
      size="xs"
      onClick={copy}
      aria-label={t("common.copyWhat", { what: label })}
      className={cn("text-muted-foreground hover:text-foreground", className)}
    >
      {copied ? (
        <CheckIcon className="text-success" aria-hidden="true" />
      ) : (
        <CopyIcon aria-hidden="true" />
      )}
      <span aria-live="polite">{copied ? t("common.copied") : t("common.copy")}</span>
    </Button>
  );
}

/** A labelled block of code (config file, command) with a copy button. */
export function CodeBlock({
  label,
  value,
  className,
}: {
  label: string;
  value: string;
  className?: string;
}) {
  const id = useId();
  return (
    <figure
      aria-labelledby={id}
      className={cn("min-w-0 overflow-hidden rounded-md border bg-muted/45", className)}
    >
      <div className="flex items-center justify-between gap-2 border-b border-border/70 py-1 pr-1 pl-3">
        <figcaption id={id} className="truncate font-mono text-[0.72rem] text-muted-foreground">
          {label}
        </figcaption>
        <CopyButton value={value} label={label} />
      </div>
      <pre
        translate="no"
        className="overflow-x-auto px-3 py-2.5 font-mono text-[0.78rem] leading-relaxed whitespace-pre text-foreground selection:bg-tape selection:text-tape-foreground"
      >
        <code>{value}</code>
      </pre>
    </figure>
  );
}
