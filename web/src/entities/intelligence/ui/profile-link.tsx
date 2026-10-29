import { Link } from "@tanstack/react-router";
import type { ComponentProps, MouseEvent } from "react";
import { cn } from "@/shared/lib/utils";
import { useProfilePanel } from "../model/profile-panel";
import { IntelligenceHandle } from "./intelligence-handle";

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
/** The same for the handle, when the sentence names one next to the name. */
const HANDLE_MARK = "\u0001";

/**
 * A translated sentence that names an intelligence ("Operated by {name}"), with the name as a link
 * that opens its profile panel (PRF-01). `sentence` gives the text for a name: it is asked for a
 * mark in the name's place and split there, so the link sits wherever the language puts the name
 * and the text of the sentence stays what it was. With `handle`, the sentence gets a second mark
 * for the handle, drawn by the handle atom inline ("Operated by {name} (@{handle})").
 */
export function NamedSentence({
  id,
  name,
  handle,
  sentence,
  className,
}: {
  id: string;
  name: string;
  /** The handle the sentence names, if it names one: it is `sentence`'s second argument. */
  handle?: string;
  sentence: (name: string, handle?: string) => string;
  className?: string;
}) {
  const text = sentence(NAME_MARK, handle === undefined ? undefined : HANDLE_MARK);
  const [before, after, ...rest] = text.split(NAME_MARK);
  if (after === undefined || rest.length > 0) return <>{sentence(name, handle)}</>;
  const handleParts = after.split(HANDLE_MARK);
  const inlineHandle = handle !== undefined && handleParts.length === 2 ? handle : undefined;
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
      {inlineHandle === undefined ? (
        after
      ) : (
        <>
          {handleParts[0]}
          <IntelligenceHandle handle={inlineHandle} />
          {handleParts[1]}
        </>
      )}
    </>
  );
}
