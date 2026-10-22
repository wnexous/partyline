import { harnessLabel } from "@/lib/agents";
import type {
  AIContact,
  Contacts,
  IntelligenceRef,
  Machine,
  MachineTree,
  TreeNode,
} from "@/lib/api/types.gen";
import { matchesTerms } from "@/lib/conversations";

/*
 * Pure functions of the contacts data (GET /contacts): what the live handlers and the mutations
 * patch in the cache, and what the contacts page and the pickers derive from it. Each patch keeps
 * the object it was given when it changes nothing, so a cache write that is a no-op is not seen.
 */

/** Replaces the summary of `ref.id` wherever the contacts hold it: the tree at any depth, the unlinked and shared AIs (and their owners), the users and the caller. */
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
  const node = (n: TreeNode): TreeNode => ({
    ...n,
    ai: ai(n.ai),
    children: n.children.map(node),
  });
  const next: Contacts = {
    ...contacts,
    me: swap(contacts.me),
    machines: contacts.machines.map((m) => ({ ...m, nodes: m.nodes.map(node) })),
    unlinked: contacts.unlinked.map(ai),
    shared: contacts.shared.map(ai),
    users: contacts.users.map(swap),
  };
  return hit ? next : contacts;
}

/** The contacts without the AI in the lists that hold it flat. The machine tree is the server's to rebuild: folders collapse differently without it. */
export function withoutAI(contacts: Contacts, id: string): Contacts {
  const keep = (c: AIContact) => c.intelligence.id !== id;
  if (contacts.unlinked.every(keep) && contacts.shared.every(keep)) return contacts;
  return {
    ...contacts,
    unlinked: contacts.unlinked.filter(keep),
    shared: contacts.shared.filter(keep),
  };
}

/** The contacts with the machine's new label; an empty label shows the hostname again (TREE-04). */
export function withMachineLabel(contacts: Contacts, machine: Machine): Contacts {
  const label = machine.label || machine.hostname;
  if (!contacts.machines.some((m) => m.hostname === machine.hostname && m.label !== label)) {
    return contacts;
  }
  return {
    ...contacts,
    machines: contacts.machines.map((m) => (m.hostname === machine.hostname ? { ...m, label } : m)),
  };
}

/** Every AI of the tree, parents before their children. */
export function flattenTree(nodes: TreeNode[]): AIContact[] {
  return nodes.flatMap((n) => [n.ai, ...flattenTree(n.children)]);
}

/**
 * The caller's own AIs as the contacts list them: the machine trees in order, parents before their
 * children, and the unlinked last. An AI shared with the caller is never one of them.
 */
export function ownAIs(contacts: Contacts): AIContact[] {
  return [...contacts.machines.flatMap((m) => flattenTree(m.nodes)), ...contacts.unlinked];
}

/**
 * The AIs the caller may merge an AI into (MRG-01): their own, the one being merged away left out,
 * wherever the contacts list it. The merge target is always the caller's, so a shared AI is never
 * offered and the owner's own list is enough.
 */
export function mergeTargets(contacts: Contacts, from: string): IntelligenceRef[] {
  return ownAIs(contacts)
    .filter((ai) => ai.intelligence.id !== from)
    .map((ai) => ai.intelligence);
}

/** Every AI the contacts list: the caller's own (machines, then unlinked) and the shared ones. */
export function allAIs(contacts: Contacts): AIContact[] {
  return [
    ...contacts.machines.flatMap((m) => flattenTree(m.nodes)),
    ...contacts.unlinked,
    ...contacts.shared,
  ];
}

/** Whether the contacts list the AI: an AI they lack was created by a session that just connected. */
export function knowsAI(contacts: Contacts, id: string): boolean {
  return allAIs(contacts).some((c) => c.intelligence.id === id);
}

/** What the owner calls each of their machines, by hostname. Other owners' machines are not in it. */
export function machineLabels(contacts: Contacts): Map<string, string> {
  return new Map(contacts.machines.map((m) => [m.hostname, m.label]));
}

/** An intelligence the user can pick (a new conversation, a group member), with the text a search looks in. */
export interface Pickable {
  ref: IntelligenceRef;
  /** Set for AIs: where they live and whether they are online. */
  ai?: AIContact;
  /** The owner's label for the AI's machine; the hostname when it has none or the AI is shared. */
  machineLabel: string;
  haystack: string;
}

/**
 * What a search of the contacts looks in for an AI: its name and @handle, the hostname of its
 * machine and the owner's label for it, its folder (as the machine has it and as `~/…`) and the
 * app it is open in.
 */
export function aiText(ai: AIContact, machineLabel: string): string {
  return [
    ai.intelligence.display_name,
    `@${ai.intelligence.handle}`,
    ai.machine,
    machineLabel,
    ai.folder,
    ai.folder_label,
    harnessLabel(ai.app) ?? ai.app,
  ]
    .filter(Boolean)
    .join("\n");
}

function pickable(ref: IntelligenceRef, ai?: AIContact, machineLabel = ""): Pickable {
  const haystack = ai ? aiText(ai, machineLabel) : `${ref.display_name}\n@${ref.handle}`;
  return { ref, ai, machineLabel, haystack };
}

/**
 * Everyone the user can message or add to a group, as the contacts list them: their own AIs (the
 * tree parents first, then the unlinked), AIs shared with them, and the other users. People are
 * left out: they are reached through a platform account, not picked from this list.
 */
export function pickables(contacts: Contacts): { ais: Pickable[]; users: Pickable[] } {
  const labels = machineLabels(contacts);
  return {
    ais: [
      ...ownAIs(contacts).map((ai) =>
        pickable(ai.intelligence, ai, labels.get(ai.machine) ?? ai.machine),
      ),
      ...contacts.shared.map((ai) => pickable(ai.intelligence, ai, ai.machine)),
    ],
    users: contacts.users.map((ref) => pickable(ref)),
  };
}

function filterNodes(nodes: TreeNode[], keep: (ai: AIContact) => boolean): TreeNode[] {
  return nodes.flatMap((n) => {
    const children = filterNodes(n.children, keep);
    return keep(n.ai) || children.length > 0 ? [{ ...n, children }] : [];
  });
}

/**
 * The contacts page's filter over the machine trees (TREE-05): a node stays when the text is
 * found in its name, handle, folder, app or machine, with every ancestor it hangs from (and none
 * of its other children); a machine whose own label or hostname matches stays whole. Every word
 * of the text has to be found, each in either place; case and accents are ignored.
 */
export function filterMachines(machines: MachineTree[], query: string): MachineTree[] {
  const text = query.trim();
  if (!text) return machines;
  return machines.flatMap((m) => {
    if (matchesTerms(`${m.label}\n${m.hostname}`, text)) return [m];
    const nodes = filterNodes(m.nodes, (ai) => matchesTerms(aiText(ai, m.label), text));
    return nodes.length > 0 ? [{ ...m, nodes }] : [];
  });
}

/** How many of the machine's AIs are online now (`online` says who is), at any depth, of how many. */
export function machineOnlineCount(
  machine: MachineTree,
  online: (ai: AIContact) => boolean,
): { online: number; total: number } {
  const ais = flattenTree(machine.nodes);
  return { online: ais.filter(online).length, total: ais.length };
}

/**
 * The machines in the order of TREE-03: those with an AI online now first, then by the owner's
 * label (the hostname when it has none), ignoring case. `online` is the live answer, so the order
 * follows presence frames that arrived after the contacts were fetched.
 */
export function sortMachines(
  machines: MachineTree[],
  online: (ai: AIContact) => boolean,
): MachineTree[] {
  const live = new Map(machines.map((m) => [m, machineOnlineCount(m, online).online > 0]));
  const name = (m: MachineTree) => m.label || m.hostname;
  return [...machines].sort(
    (a, b) =>
      Number(live.get(b)) - Number(live.get(a)) ||
      name(a).localeCompare(name(b), undefined, { sensitivity: "base" }),
  );
}
