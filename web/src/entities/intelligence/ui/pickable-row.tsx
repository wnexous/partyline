import type { ReactNode } from "react";
import type { Pickable } from "../lib/contacts";
import { useIsOnline } from "../model/presence";
import { PickableMeta } from "./contact-meta";
import { IntelligenceRow } from "./intelligence-row";

/**
 * One candidate of a picker (a group member, a DM target): the `IntelligenceRow` of the person or
 * AI, with where it lives and its presence read from the one rule (`useIsOnline`). The picker
 * supplies its own list and control (`action`, or the row wrapped in a checkbox label).
 */
export function PickableRow({
  pickable,
  as = "li",
  profileLink = false,
  testId,
  action,
  className,
}: {
  pickable: Pickable;
  as?: "li" | "div";
  /** The name links to the profile (the row is the target); off in a picker that selects. */
  profileLink?: boolean;
  testId?: string;
  action?: ReactNode;
  className?: string;
}) {
  const online = useIsOnline(pickable.ref.id, pickable.ai?.online);
  return (
    <IntelligenceRow
      intelligence={pickable.ref}
      as={as}
      testId={testId}
      profileLink={profileLink}
      showHandle
      meta={<PickableMeta pickable={pickable} online={online} />}
      actions={action}
      className={className}
    />
  );
}
