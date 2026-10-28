import { CompassIcon, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { EmptyState } from "@/shared/ui/molecules/empty-state";

/**
 * "This does not exist" (or is not yours to see): an empty state with what it was about, one line of
 * why, and the way back. The caller translates and gives the action (a link back, usually).
 */
export function NotFound({
  title,
  body,
  action,
  icon = CompassIcon,
}: {
  title: ReactNode;
  body: ReactNode;
  action?: ReactNode;
  icon?: LucideIcon;
}) {
  return (
    <EmptyState icon={icon} title={title} action={action}>
      <p>{body}</p>
    </EmptyState>
  );
}
