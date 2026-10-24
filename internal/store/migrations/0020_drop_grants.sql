-- Drop the grants table (organizations feature, AD-016, AD-012). Sharing is organization membership:
-- the Go hook of 0019 turned every grant of a user into a membership and emptied the table in the
-- same transaction, and no code reads or writes it any more. Nothing references the table, so
-- dropping it (and the index on its subject) takes nothing else with it. A database that ran the
-- 0019 hook and has this file pending migrates both in one transaction (MIG-05).

DROP INDEX grants_subject;
DROP TABLE grants;
