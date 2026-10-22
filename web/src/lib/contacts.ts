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
