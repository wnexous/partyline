import { Link } from "@tanstack/react-router";
import type { ComponentProps, MouseEvent } from "react";
import { useProfilePanel } from "@/hooks/use-profile-panel";
import { cn } from "@/lib/utils";

/*
 * An identity that can be clicked (PRF-01, PRF-06). It is a link to the identity's profile page,
 * so the browser's own gestures keep working (Ctrl, Cmd or Shift click, the middle button: the
 * page in a new tab). A plain click, or Enter, opens the profile panel instead and stays on the
 * page.
 */

/** A click the browser would turn into "open this in a new tab or window". */
export const opensElsewhere = (event: MouseEvent) =>
  event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey;

type ProfileLinkProps = Omit<ComponentProps<typeof Link>, "to" | "params" | "search" | "hash"> & {
  /** The id of the intelligence whose profile the link opens. */
  id: string;
};

export function ProfileLink({ id, onClick, ...props }: ProfileLinkProps) {
  const profilePanel = useProfilePanel();
  return (
    <Link
      {...props}
      to="/contacts/$id"
      params={{ id }}
      onClick={(event) => {
        onClick?.(event);
        if (event.defaultPrevented || opensElsewhere(event)) return;
        event.preventDefault();
        profilePanel.open(id, event.currentTarget);
      }}
    />
  );
}

/** What a translated sentence is written around, to find where the name goes in it. */
const NAME_MARK = "\u0000";

/**
 * A translated sentence that names an intelligence ("Operated by {name}"), with the name as a link
 * that opens its profile panel (PRF-01). `sentence` gives the text for a name: it is asked for a
 * mark in the name's place and split there, so the link sits wherever the language puts the name
 * and the text of the sentence stays what it was.
 */
export function NamedSentence({
  id,
  name,
  sentence,
  className,
}: {
  id: string;
  name: string;
  sentence: (name: string) => string;
  className?: string;
}) {
  const [before, after, ...rest] = sentence(NAME_MARK).split(NAME_MARK);
  if (after === undefined || rest.length > 0) return <>{sentence(name)}</>;
  return (
    <>
      {before}
      <ProfileLink
        id={id}
        className={cn(
          "rounded-sm text-link underline underline-offset-4 outline-none hover:decoration-2 focus-visible:ring-2 focus-visible:ring-ring",
          className,
        )}
      >
        {name}
      </ProfileLink>
      {after}
    </>
  );
}
