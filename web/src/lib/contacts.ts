import { harnessLabel } from "@/lib/agents";
import type { AIContact, Contacts, IntelligenceRef, OrgAIs } from "@/lib/api/types.gen";
import { matchesTerms } from "@/lib/conversations";

/*
 * Pure functions of the contacts data (GET /contacts): what the live handlers and the mutations
 * patch in the cache, and what the contacts page and the pickers derive from it. Each patch keeps
 * the object it was given when it changes nothing, so a cache write that is a no-op is not seen.
 */

/** Replaces the summary of `ref.id` wherever the contacts hold it: the AIs of every organization and the hosted ones (and their hosts), the users and the caller. */
export function withIntelligence(contacts: Contacts, ref: IntelligenceRef): Contacts {
  let hit = false;
  const swap = (r: IntelligenceRef): IntelligenceRef => {
    if (r.id !== ref.id) return r;
    hit = true;
    return ref;
  };
  const ai = (c: AIContact): AIContact => ({
    ...c,
    intelligence: swap(c.intelligence),
    ...(c.owner ? { owner: swap(c.owner) } : {}),
  });
  const next: Contacts = {
    ...contacts,
    me: swap(contacts.me),
    org_ais: contacts.org_ais.map((g) => ({ ...g, ais: g.ais.map(ai) })),
    hosted: contacts.hosted.map(ai),
    users: contacts.users.map(swap),
  };
  return hit ? next : contacts;
}

/** The contacts without the AI, in every list that holds it. */
export function withoutAI(contacts: Contacts, id: string): Contacts {
  const keep = (c: AIContact) => c.intelligence.id !== id;
  if (contacts.hosted.every(keep) && contacts.org_ais.every((g) => g.ais.every(keep))) {
    return contacts;
  }
  return {
    ...contacts,
    org_ais: contacts.org_ais.map((g) =>
      g.ais.every(keep) ? g : { ...g, ais: g.ais.filter(keep) },
    ),
    hosted: contacts.hosted.every(keep) ? contacts.hosted : contacts.hosted.filter(keep),
  };
}

/**
 * Every AI the contacts list, once each: those of the caller's organizations, group by group
 * (an AI that belongs to two of them is listed in the first), then the hosted ones no group lists.
 */
export function allAIs(contacts: Contacts): AIContact[] {
  const seen = new Set<string>();
  return [...contacts.org_ais.flatMap((g) => g.ais), ...contacts.hosted].filter((ai) => {
    if (seen.has(ai.intelligence.id)) return false;
    seen.add(ai.intelligence.id);
    return true;
  });
}

/** The AIs the caller hosts: the contacts name the host of the AIs somebody else hosts. */
export function ownAIs(contacts: Contacts): AIContact[] {
  return allAIs(contacts).filter((ai) => !ai.owner);
}

/**
 * The AIs the caller may merge an AI into (MRG-01): their own that belong to the same home
 * organization, the one being merged away left out. The server decides what the caller manages.
 */
export function mergeTargets(contacts: Contacts, from: string): IntelligenceRef[] {
  const all = allAIs(contacts);
  const home = all.find((ai) => ai.intelligence.id === from)?.org_id;
  return ownAIs(contacts)
    .filter((ai) => ai.intelligence.id !== from && (!home || ai.org_id === home))
    .map((ai) => ai.intelligence);
}

/** Whether the contacts list the AI: an AI they lack was created by a session that just connected. */
export function knowsAI(contacts: Contacts, id: string): boolean {
  return allAIs(contacts).some((c) => c.intelligence.id === id);
}

/** An intelligence the user can pick (a new conversation, a group member), with the text a search looks in. */
export interface Pickable {
  ref: IntelligenceRef;
  /** Set for AIs: where they live and whether they are online. */
  ai?: AIContact;
  haystack: string;
}

/**
 * What a search of the contacts looks in for an AI: its name and @handle, its folder (as the
 * host has it and as `~/…`) and the app it is open in.
 */
export function aiText(ai: AIContact): string {
  return [
    ai.intelligence.display_name,
    `@${ai.intelligence.handle}`,
    ai.folder,
    ai.folder_label,
    harnessLabel(ai.app) ?? ai.app,
  ]
    .filter(Boolean)
    .join("\n");
}

function pickable(ref: IntelligenceRef, ai?: AIContact): Pickable {
  const haystack = ai ? aiText(ai) : `${ref.display_name}\n@${ref.handle}`;
  return { ref, ai, haystack };
}

/**
 * Everyone the user can message or add to a group, as the contacts list them: the AIs of their
 * organizations and the ones they host, and the other users. People are left out: they are
 * reached through a platform account, not picked from this list.
 *
 * For a group of an organization (ORG-03) name it: the AIs are then those that are members of it,
 * since a group is made of the members of its organization. The users stay the instance's, because
 * the contacts do not say which organizations they belong to; the server refuses the ones who are
 * not members (`not_org_member`).
 */
export function pickables(
  contacts: Contacts,
  orgId?: string,
): { ais: Pickable[]; users: Pickable[] } {
  const ais =
    orgId === undefined
      ? allAIs(contacts)
      : (contacts.org_ais.find((g) => g.org.id === orgId)?.ais ?? []);
  return {
    ais: ais.map((ai) => pickable(ai.intelligence, ai)),
    users: contacts.users.map((ref) => pickable(ref)),
  };
}

/**
 * The contacts page's filter over the organization groups: an AI stays when the text is found in
 * its name, handle, folder or app, and a group stays when its organization's name matches (whole)
 * or one of its AIs does. Every word of the text has to be found, each in either place; case and
 * accents are ignored. Without text nothing is filtered: empty groups are the page's to hide.
 */
export function filterOrgs(groups: OrgAIs[], query: string): OrgAIs[] {
  const text = query.trim();
  if (!text) return groups;
  return groups.flatMap((g) => {
    const org = `${g.org.name}\n@${g.org.slug}`;
    if (matchesTerms(org, text)) return [g];
    const ais = g.ais.filter((ai) => matchesTerms(`${org}\n${aiText(ai)}`, text));
    return ais.length > 0 ? [{ ...g, ais }] : [];
  });
}

/** How many of the AIs are online now (`online` says who is), of how many. */
export function onlineCount(
  ais: AIContact[],
  online: (ai: AIContact) => boolean,
): { online: number; total: number } {
  return { online: ais.filter(online).length, total: ais.length };
}
