import { Link } from "@tanstack/react-router";
import { BuildingIcon, Settings2Icon } from "lucide-react";
import { type MyOrg, permissionLabel } from "@/entities/organization";
import { useT } from "@/shared/i18n";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";
import { OrgPageItems } from "./org-page-items";

/*
 * The organization's entry (SET-09, SET-11): the pages of the organization the switcher shows,
 * behind the gear beside the full switcher and, on a phone's top bar where the gear does not fit,
 * inside the compact switcher's own menu. One list feeds both, so the two can never disagree.
 */

/** A page of an organization: what it is called, its icon, where it lives and who may open it. */
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
