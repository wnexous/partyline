import { Link } from "@tanstack/react-router";
import { BuildingIcon, ChevronsUpDownIcon, PlusIcon } from "lucide-react";
import { useId, useMemo, useState } from "react";
import {
  type MyOrg,
  OrgAvatar,
  permissionLabel,
  unreadByOrg,
  unreadOutsideOrg,
  useCurrentOrg,
} from "@/entities/organization";
import { useConversations } from "@/entities/room";
import { NewOrgDialog } from "@/features/create-org";
import { type Translate, useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { Button } from "@/shared/ui/primitives/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/shared/ui/primitives/dropdown-menu";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { OrgPageItems } from "./org-page-items";

/*
 * The organization switcher (ORG-01, ORG-02): the organization the sidebar, the connections and
 * the new groups are about, with the others one pick away. Each organization shows what the
 * viewer is in it and how many messages wait unread in it (a DM two organizations share counts in
 * both); the button itself carries what waits outside the current one, so nothing is missed. The
 * pick is kept in this browser (hooks/use-current-org.ts).
 */

const unreadText = (t: Translate, count: number) =>
  t(count === 1 ? "conversations.unreadOne" : "conversations.unreadMany", { count });

/** The count a badge shows: 99+ from a hundred on; screen readers get the full number elsewhere. */
const capped = (count: number) => (count > 99 ? "99+" : String(count));

export function OrgSwitcher({
  compact = false,
  className,
}: {
  /** Only the organization's picture on the button: the top bar of a phone has no room for a name. */
  compact?: boolean;
  className?: string;
}) {
  const t = useT();
  const labelId = useId();
  const { org, orgs, loading, failed, select } = useCurrentOrg();
  const conversations = useConversations().data;
  const [creating, setCreating] = useState(false);
  const unread = useMemo(() => unreadByOrg(conversations ?? [], orgs), [conversations, orgs]);
  const elsewhere = useMemo(
    () => (org ? unreadOutsideOrg(conversations ?? [], org.id) : 0),
    [conversations, org],
  );

  if (loading) {
    return (
      <Skeleton
        aria-hidden="true"
        className={cn(compact ? "size-11" : "h-11 w-full md:h-9", className)}
      />
    );
  }
  // An unreadable list is not a viewer without organizations: say nothing, hide nothing else.
  if (failed) return null;

  const dialog = <NewOrgDialog open={creating} onOpenChange={setCreating} />;
  // Belonging to none, the one thing to do is to start one.
  if (!org) {
    return (
      <>
        <Button
          variant="outline"
          className={cn("h-11 w-full justify-start md:h-9", className)}
          onClick={() => setCreating(true)}
        >
          <PlusIcon aria-hidden="true" />
          {t("orgs.new")}
        </Button>
        {dialog}
      </>
    );
  }

  const label =
    elsewhere === 0
      ? t("orgs.switcher", { org: org.name })
      : t(elsewhere === 1 ? "orgs.switcherUnreadOne" : "orgs.switcherUnreadMany", {
          org: org.name,
          count: elsewhere,
        });

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="outline"
            aria-label={label}
            className={cn(
              "relative min-w-0 justify-start gap-2 px-2",
              compact ? "size-11 justify-center p-0" : "h-11 w-full md:h-9",
              className,
            )}
          >
            <OrgAvatar org={org} size="sm" />
            {compact ? null : (
              <>
                <span className="min-w-0 flex-1 truncate text-left">{org.name}</span>
                <ChevronsUpDownIcon className="size-4 text-muted-foreground" aria-hidden="true" />
              </>
            )}
            {elsewhere > 0 ? (
              <span
                aria-hidden="true"
                data-testid="elsewhere-count"
                className={cn(
                  "inline-flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full bg-primary px-1.5 text-[0.7rem] font-semibold text-primary-foreground tabular",
                  compact && "absolute -top-1 -right-1",
                )}
              >
                {capped(elsewhere)}
              </span>
            ) : null}
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent
          align={compact ? "end" : "start"}
          aria-labelledby={labelId}
          className="max-h-[min(24rem,var(--radix-dropdown-menu-content-available-height))] w-72 max-w-[calc(100vw-1rem)] overflow-y-auto"
        >
          <DropdownMenuLabel
            id={labelId}
            className="label-caps py-1 text-[0.68rem] text-muted-foreground"
          >
            {t("orgs.menu")}
          </DropdownMenuLabel>
          <DropdownMenuRadioGroup value={org.id} onValueChange={select}>
            {orgs.map((o) => (
              <OrgItem key={o.id} org={o} unread={unread[o.id] ?? 0} />
            ))}
          </DropdownMenuRadioGroup>
          {/*
            On a phone's top bar the switcher is the organization's entry, because no gear fits
            there; on a wide screen the gear beside it lists the same pages (SET-09).
          */}
          {compact ? (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuGroup>
                {/* A long name is cut, not wrapped or spilled: the menu keeps its width (SET-09). */}
                <DropdownMenuLabel className="label-caps py-1 text-[0.68rem] text-muted-foreground">
                  <span className="block truncate">{t("nav.orgMenu", { org: org.name })}</span>
                </DropdownMenuLabel>
                <OrgPageItems org={org} />
              </DropdownMenuGroup>
            </>
          ) : null}
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild>
            <Link to="/orgs">
              <BuildingIcon aria-hidden="true" />
              {t("orgs.all")}
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => setCreating(true)}>
            <PlusIcon aria-hidden="true" />
            {t("orgs.new")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {dialog}
    </>
  );
}

/** One organization of the menu: its face, its name, what the viewer is in it and what waits there. */
function OrgItem({ org, unread }: { org: MyOrg; unread: number }) {
  const t = useT();
  const role = permissionLabel(t, org.permission);
  const label =
    unread > 0
      ? t("orgs.itemUnread", { org: org.name, role, unread: unreadText(t, unread) })
      : t("orgs.item", { org: org.name, role });
  return (
    <DropdownMenuRadioItem value={org.id} aria-label={label} className="min-h-11 gap-3 md:min-h-9">
      <OrgAvatar org={org} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{org.name}</span>
        <span className="block truncate text-xs text-muted-foreground">{role}</span>
      </span>
      {unread > 0 ? (
        <span
          aria-hidden="true"
          className="inline-flex h-5 min-w-5 shrink-0 items-center justify-center rounded-full bg-primary px-1.5 text-[0.7rem] font-semibold text-primary-foreground tabular"
        >
          {capped(unread)}
        </span>
      ) : null}
    </DropdownMenuRadioItem>
  );
}
