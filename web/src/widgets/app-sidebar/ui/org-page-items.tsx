import { Link } from "@tanstack/react-router";
import { type LucideIcon, PlugZapIcon, SettingsIcon, UsersIcon } from "lucide-react";
import type { MyOrg } from "@/entities/organization";
import { type MessageKey, useT } from "@/shared/i18n";
import { DropdownMenuItem } from "@/shared/ui/primitives/dropdown-menu";

export interface OrgPage {
  to: "/orgs/$orgId/members" | "/orgs/$orgId/connections" | "/orgs/$orgId/settings";
  label: MessageKey;
  icon: LucideIcon;
}

/** The pages of an organization, in the design's order; every member opens each of them. */
const ORG_PAGE_ITEMS: OrgPage[] = [
  { to: "/orgs/$orgId/members", label: "orgs.tabMembers", icon: UsersIcon },
  { to: "/orgs/$orgId/connections", label: "orgs.tabConnectors", icon: PlugZapIcon },
  { to: "/orgs/$orgId/settings", label: "orgs.tabSettings", icon: SettingsIcon },
];

/** The organization's pages as menu rows, every one naming `org`; a finger's height on touch. */
export function OrgPageItems({ org }: { org: MyOrg }) {
  const t = useT();
  return (
    <>
      {ORG_PAGE_ITEMS.map((page) => (
        <DropdownMenuItem key={page.to} asChild>
          <Link to={page.to} params={{ orgId: org.id }} className="min-h-11 md:min-h-9">
            <page.icon aria-hidden="true" />
            {t(page.label)}
          </Link>
        </DropdownMenuItem>
      ))}
    </>
  );
}
