import type { Contacts } from "@/lib/api/types.gen";
import {
  makeAI,
  makeAIContact,
  makeContacts,
  makeIntelligence,
  makeMachineTree,
  makePerson,
  makeTreeNode,
} from "./fixtures";

/**
 * One user's contacts, shared by the tests of everything that lists them: two machines (devbox,
 * labelled Laptop for the second), Atlas with Notes nested in its folder, Lab under a collapsed
 * path, an unlinked AI, an AI shared by Bob, two other users and one connector with people.
 */

export const atlas = makeAI();
export const notes = makeAI({ id: "a2", display_name: "Notes", handle: "notes" });
export const lab = makeAI({ id: "a3", display_name: "Lab", handle: "lab" });
export const oldHelper = makeAI({ id: "a4", display_name: "Old helper", handle: "old-helper" });
export const sage = makeAI({ id: "a5", display_name: "Sage", handle: "sage" });
export const bob = makeIntelligence({ id: "u2", display_name: "Bob Lima", handle: "bob" });
export const cora = makeIntelligence({ id: "u3", display_name: "Cora Dias", handle: "cora" });
export const lia = makePerson();
export const liam = makePerson({ id: "p2", display_name: "Liam Reis", handle: "liam" });

export const sampleContacts: Contacts = makeContacts({
  machines: [
    makeMachineTree({
      hostname: "devbox",
      label: "devbox",
      online: true,
      nodes: [
        makeTreeNode({
          ai: makeAIContact({ intelligence: atlas, online: true }),
          children: [
            makeTreeNode({
              ai: makeAIContact({
                intelligence: notes,
                app: "opencode",
                folder: "/home/dev/atlas/notes",
                folder_label: "~/atlas/notes",
              }),
              name: "notes",
            }),
          ],
        }),
      ],
    }),
    makeMachineTree({
      hostname: "notebook-01",
      label: "Laptop",
      nodes: [
        makeTreeNode({
          ai: makeAIContact({
            intelligence: lab,
            machine: "notebook-01",
            app: "codex",
            folder: "/home/ana/c/d/lab",
            folder_label: "~/c/d/lab",
          }),
          path: "c/d",
          name: "lab",
        }),
      ],
    }),
  ],
  unlinked: [makeAIContact({ intelligence: oldHelper, folder: "", folder_label: "" })],
  shared: [
    makeAIContact({
      intelligence: sage,
      owner: bob,
      machine: "cloud",
      folder: "/srv/sage",
      folder_label: "/srv/sage",
    }),
  ],
  users: [bob, cora],
  people: [
    { connector: { id: "k1", kind: "whatsapp", name: "ops-line" }, count: 2 },
    { connector: { id: "k2", kind: "telegram", name: "quiet-bot" }, count: 0 },
  ],
});
