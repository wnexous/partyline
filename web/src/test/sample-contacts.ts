import type { Contacts } from "@/shared/api/types.gen";
import {
  makeAI,
  makeAIContact,
  makeContacts,
  makeIntelligence,
  makeOrgAIs,
  makePerson,
} from "./fixtures";

/**
 * One user's contacts, shared by the tests of everything that lists them: two organizations (Acme
 * with Atlas, Notes and Sage, hosted by Bob; Globex with Lab), an AI hosted by the user that no
 * organization lists, two other users and one connection with people.
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
  org_ais: [
    makeOrgAIs({
      org: { id: "o1", name: "Acme", slug: "acme" },
      ais: [
        makeAIContact({ intelligence: atlas, online: true }),
        makeAIContact({
          intelligence: notes,
          app: "opencode",
          folder: "/home/dev/atlas/notes",
          folder_label: "~/atlas/notes",
        }),
        makeAIContact({
          intelligence: sage,
          owner: bob,
          folder: "/srv/sage",
          folder_label: "/srv/sage",
        }),
      ],
    }),
    makeOrgAIs({
      org: { id: "o2", name: "Globex", slug: "globex" },
      ais: [
        makeAIContact({
          intelligence: lab,
          org_id: "o2",
          app: "codex",
          folder: "/home/ana/c/d/lab",
          folder_label: "~/c/d/lab",
        }),
      ],
    }),
  ],
  hosted: [makeAIContact({ intelligence: oldHelper, org_id: "o3", folder: "", folder_label: "" })],
  users: [bob, cora],
  people: [
    { connector: { id: "k1", kind: "whatsapp", name: "ops-line" }, count: 2 },
    { connector: { id: "k2", kind: "telegram", name: "quiet-bot" }, count: 0 },
  ],
});
