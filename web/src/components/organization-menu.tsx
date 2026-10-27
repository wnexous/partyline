import { Link } from "@tanstack/react-router";
import {
  BuildingIcon,
  type LucideIcon,
  NetworkIcon,
  PlugZapIcon,
  Settings2Icon,
  SettingsIcon,
  UsersIcon,
  WaypointsIcon,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { type MessageKey, useT } from "@/lib/i18n";
import { isAdminOf, type MyOrg, permissionLabel } from "@/lib/orgs";

/*
 * The organization's entry (SET-09, SET-11): the pages of the organization the switcher shows,
 * behind the gear beside the full switcher and, on a phone's top bar where the gear does not fit,
 * inside the compact switcher's own menu. One list feeds both, so the two can never disagree.
 */

/** A page of an organization: what it is called, its icon, where it lives and who may open it. */
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

/**
 * The gear beside the full organization switcher: it names the organization and the viewer's part
 * in it, lists its pages and ends with all of them. Its name says what it opens, so a screen reader
 * can tell it from the switcher next to it.
 */
export function OrganizationMenu({ org }: { org: MyOrg }) {
  const t = useT();
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={t("nav.orgMenu", { org: org.name })}
          className="size-11 shrink-0 md:size-9"
        >
          <Settings2Icon aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        aria-label={t("nav.orgMenu", { org: org.name })}
        className="w-72 max-w-[calc(100vw-1rem)]"
      >
        <DropdownMenuLabel className="font-normal">
          <span className="block truncate font-semibold text-foreground">{org.name}</span>
          <span className="block truncate font-mono text-xs text-muted-foreground">
            {permissionLabel(t, org.permission)}
          </span>
        </DropdownMenuLabel>
        <OrgPageItems org={org} />
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/orgs" className="min-h-11 md:min-h-9">
            <BuildingIcon aria-hidden="true" />
            {t("orgs.all")}
          </Link>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
