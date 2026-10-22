import { harnessLabel } from "@/lib/agents";
import type { AIContact, Contacts, IntelligenceRef, Machine, TreeNode } from "@/lib/api/types.gen";

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

function pickable(ref: IntelligenceRef, ai?: AIContact, machineLabel = ""): Pickable {
  const parts = [ref.display_name, `@${ref.handle}`];
  if (ai) {
    parts.push(
      ai.machine,
      machineLabel,
      ai.folder,
      ai.folder_label,
      harnessLabel(ai.app) ?? ai.app,
    );
  }
  return { ref, ai, machineLabel, haystack: parts.filter(Boolean).join("\n") };
}

/**
 * Everyone the user can message or add to a group, as the contacts list them: their own AIs (the
 * tree parents first, then the unlinked), AIs shared with them, and the other users. People are
 * left out: they are reached through a platform account, not picked from this list.
 */
export function pickables(contacts: Contacts): { ais: Pickable[]; users: Pickable[] } {
  const labels = machineLabels(contacts);
  const own = [...contacts.machines.flatMap((m) => flattenTree(m.nodes)), ...contacts.unlinked];
  return {
    ais: [
      ...own.map((ai) => pickable(ai.intelligence, ai, labels.get(ai.machine) ?? ai.machine)),
      ...contacts.shared.map((ai) => pickable(ai.intelligence, ai, ai.machine)),
    ],
    users: contacts.users.map((ref) => pickable(ref)),
  };
}
