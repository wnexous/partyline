import { Skeleton } from "@/shared/ui/primitives/skeleton";

/**
 * The rows' place while a list loads, hidden from assistive tech. `avatar` puts a round face at the
 * start of each row (the conversation list); without it each row is two bars (contacts, connections).
 */
export function ListSkeleton({ rows = 3, avatar = false }: { rows?: number; avatar?: boolean }) {
  const items = Array.from({ length: rows }, (_, i) => i);
  if (avatar) {
    return (
      <ul aria-hidden="true" className="space-y-1 py-1">
        {items.map((i) => (
          <li
            key={i}
            className="grid grid-cols-[2.5rem_minmax(0,1fr)] items-center gap-x-3 px-3 py-2.5"
          >
            <Skeleton className="size-10 rounded-full" />
            <div className="space-y-2">
              <Skeleton className="h-3.5 w-32" />
              <Skeleton className="h-3 w-48 max-w-full" />
            </div>
          </li>
        ))}
      </ul>
    );
  }
  return (
    <ul aria-hidden="true" className="divide-y border-b">
      {items.map((i) => (
        <li key={i} className="space-y-2 px-4 py-4 md:px-8">
          <Skeleton className="h-4 w-40" />
          <Skeleton className="h-3 w-56 max-w-full" />
        </li>
      ))}
    </ul>
  );
}
