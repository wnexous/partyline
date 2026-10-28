import { createFileRoute } from "@tanstack/react-router";
import { ContactsPage } from "@/pages/contacts";

export const Route = createFileRoute("/_app/contacts/")({
  component: ContactsPage,
});
