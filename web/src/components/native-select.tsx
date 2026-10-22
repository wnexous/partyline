import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

/**
 * A native `<select>`, styled like the app's inputs. The platform's own picker opens on a phone,
 * which is easier to use than a custom listbox (UI-24) and gets the browser's keyboard behaviour.
 */
export function NativeSelect({ className, ...props }: ComponentProps<"select">) {
  return (
    <select
      className={cn(
        "h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:bg-input/30",
        className,
      )}
      {...props}
    />
  );
}
