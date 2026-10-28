import { Link } from "@tanstack/react-router";
import {
  type LucideIcon,
  NetworkIcon,
  PlugZapIcon,
  SettingsIcon,
  UsersIcon,
  WaypointsIcon,
} from "lucide-react";
import type { MyOrg } from "@/entities/organization";
import { isAdminOf } from "@/entities/organization";
import { type MessageKey, useT } from "@/shared/i18n";
import { DropdownMenuItem } from "@/shared/ui/primitives/dropdown-menu";

export interface OrgPage {
  to:
    | "/orgs/$orgId/chart"
    | "/orgs/$orgId/members"
    | "/orgs/$orgId/positions"
    | "/orgs/$orgId/connections"
    | "/orgs/$orgId/settings";
  label: MessageKey;
  icon: LucideIcon;
  /** The positions' editor is an admin's page (OMEM-04). */
  adminOnly?: boolean;
}

const ORG_PAGE_ITEMS: OrgPage[] = [
  { to: "/orgs/$orgId/chart", label: "orgs.tabChart", icon: NetworkIcon },
  { to: "/orgs/$orgId/members", label: "orgs.tabMembers", icon: UsersIcon },
  {
    to: "/orgs/$orgId/positions",
    label: "orgs.tabPositions",
    icon: WaypointsIcon,
    adminOnly: true,
  },
  { to: "/orgs/$orgId/connections", label: "orgs.tabConnectors", icon: PlugZapIcon },
  { to: "/orgs/$orgId/settings", label: "orgs.tabSettings", icon: SettingsIcon },
];

/** The pages of `org` the viewer may open, in the design's order: positions only for an admin. */
export function ORG_PAGES(org: MyOrg): OrgPage[] {
  const admin = isAdminOf(org);
  return ORG_PAGE_ITEMS.filter((page) => !page.adminOnly || admin);
}

/** The organization's pages as menu rows, every one naming `org`; a finger's height on touch. */
export function OrgPageItems({ org }: { org: MyOrg }) {
  const t = useT();
  return (
    <>
      {ORG_PAGES(org).map((page) => (
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
