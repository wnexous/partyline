import { zodResolver } from "@hookform/resolvers/zod";
import { useNavigate } from "@tanstack/react-router";
import { SearchIcon, XIcon } from "lucide-react";
import { type ReactNode, useEffect, useId, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { PickableMeta } from "@/components/contact-meta";
import { FormAlert, TextField } from "@/components/form-fields";
import { IntelligenceAvatar } from "@/components/intelligence/intelligence-avatar";
import { IntelligenceName } from "@/components/intelligence/intelligence-name";
import { OrgSelect } from "@/components/org-select";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Form } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useCurrentOrg } from "@/hooks/use-current-org";
import { useOrgUsers } from "@/hooks/use-org-users";
import { isApiError } from "@/lib/api/client";
import { useContacts, useCreateGroup } from "@/lib/api/queries";
import {
  ErrorCodeMembersRefused,
  ErrorCodeOrganizationRequired,
  MaxGroupMembers,
  type RefusedMember,
  type RefusedReason,
  RefusedReasonNotOrgMember,
  RefusedReasonPerson,
} from "@/lib/api/types.gen";
import { type Pickable, pickableList } from "@/lib/contacts";
import { matchesTerms } from "@/lib/conversations";
import { applyFieldErrors } from "@/lib/forms";
import { useT } from "@/lib/i18n";
import { useLiveStore } from "@/lib/live/store";
import { canCreateGroupsIn } from "@/lib/orgs";
import { withContactPresence } from "@/lib/presence";
import { type CreateGroupForm, createGroupSchema } from "@/lib/schemas";

/*
 * "New group" (LAY-04): a name, the organization it belongs to, and the members that join, AIs and
 * users in one list by name (AD-024). A group belongs to one organization (ORG-02) the viewer can
 * create groups in: not one they are only a guest of (OMEM-04). Its members are the organization's.
 * The server applies the add-member rules of the creator to each member and refuses the whole group
 * when any fails (a user who is no longer a member of the organization, for one); the dialog then
 * names who was refused and why, marks their rows, and keeps everything else as typed.
 */

export function NewGroupDialog({ trigger }: { trigger: ReactNode }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>{trigger}</DialogTrigger>
      <DialogContent className="flex max-h-[min(44rem,calc(100dvh-2rem))] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("newGroup.title")}</DialogTitle>
          <DialogDescription>{t("newGroup.body")}</DialogDescription>
        </DialogHeader>
        {/* Mounted only while open: every opening starts with an empty form. */}
        {open ? <GroupForm onDone={() => setOpen(false)} onCancel={() => setOpen(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function GroupForm({ onDone, onCancel }: { onDone: () => void; onCancel: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const contacts = useContacts();
  const create = useCreateGroup();
  const { org: current, orgs, loading: orgsLoading, select } = useCurrentOrg();
  const presence = useLiveStore((s) => s.presence);
  const filterId = useId();
  const [filter, setFilter] = useState("");
  // Ids in the order they were picked: the server adds the members in that order.
  const [picked, setPicked] = useState<string[]>([]);
  const [refused, setRefused] = useState<RefusedMember[]>([]);
  const [formError, setFormError] = useState<string | null>(null);
  const [pickedOrg, setPickedOrg] = useState<string | null>(null);
  const form = useForm<CreateGroupForm>({
    resolver: zodResolver(createGroupSchema),
    defaultValues: { name: "" },
  });

  // The organization the group goes to: the pick, else the current one when the viewer can create
  // groups there, else the first where they can. None while their organizations are unknown: the
  // server then uses their default and decides whether they may.
  const creatable = useMemo(() => orgs.filter(canCreateGroupsIn), [orgs]);
  const org =
    creatable.find((o) => o.id === pickedOrg) ??
    creatable.find((o) => o.id === current?.id) ??
    creatable[0];
  const guestEverywhere = orgs.length > 0 && creatable.length === 0;
  const orgId = org?.id;
  const { users: orgUsers, loading: usersLoading } = useOrgUsers(orgId);
  // Nothing is offered until the organization's users are known, so the instance's are not listed first.
  const all = useMemo(
    () => (contacts.data && !usersLoading ? pickableList(contacts.data, orgId, orgUsers) : null),
    [contacts.data, orgId, orgUsers, usersLoading],
  );
  const byId = useMemo(() => new Map((all ?? []).map((p) => [p.ref.id, p])), [all]);
  const text = filter.trim();
  const found = (all ?? []).filter((p) => matchesTerms(p.haystack, text));
  const total = all?.length ?? 0;
  const full = picked.length >= MaxGroupMembers;

  const toggle = (id: string, on: boolean) => {
    setPicked((now) =>
      on ? (now.includes(id) ? now : [...now, id]) : now.filter((x) => x !== id),
    );
  };

  // Another organization offers other AIs and users: whoever is picked and no longer offered goes
  // (once the new offer is known), and what the server said about the old choice no longer applies.
  useEffect(() => {
    if (!all) return;
    const offered = new Set(all.map((p) => p.ref.id));
    setPicked((now) =>
      now.every((id) => offered.has(id)) ? now : now.filter((id) => offered.has(id)),
    );
  }, [all]);
  const changeOrg = (next: string) => {
    setPickedOrg(next);
    setRefused([]);
    setFormError(null);
  };

  const refusalText = (reason: RefusedReason) => {
    if (reason === RefusedReasonPerson) return t("newGroup.refusedPerson");
    if (reason === RefusedReasonNotOrgMember) {
      return org
        ? t("newGroup.refusedNotOrgMember", { org: org.name })
        : t("newGroup.refusedNotOrgMemberUnknown");
    }
    return t("newGroup.refusedNotFound");
  };

  // A refusal stays until the user takes the refused members out; an id the contacts do not know
  // cannot be taken out, so it stays until the next try.
  const stillRefused = refused.filter((r) => picked.includes(r.id) || !byId.has(r.id));
  const flagged = new Set(stillRefused.map((r) => r.id));

  const onSubmit = form.handleSubmit(async (values) => {
    if (create.isPending) return;
    setFormError(null);
    setRefused([]);
    try {
      const room = await create.mutateAsync({
        name: createGroupSchema.parse(values).name,
        members: picked,
        ...(org ? { org_id: org.id } : {}),
      });
      // The list is the current organization's: follow the group to its organization.
      if (org && org.id !== current?.id) select(org.id);
      onDone();
      void navigate({ to: "/rooms/$roomId", params: { roomId: room.id } });
    } catch (err) {
      if (isApiError(err) && err.code === ErrorCodeMembersRefused && err.refused.length > 0) {
        setRefused(err.refused);
        return;
      }
      if (isApiError(err) && err.code === ErrorCodeOrganizationRequired) {
        setFormError(t("newGroup.noOrg"));
        return;
      }
      if (isApiError(err) && err.status === 403 && org) {
        setFormError(t("newGroup.forbidden", { org: org.name }));
        return;
      }
      if (applyFieldErrors(err, form.setError, ["name"])) return;
      setFormError(isApiError(err) && err.status === 0 ? t("error.network") : t("newGroup.failed"));
    }
  });

  return (
    <Form {...form}>
      <form onSubmit={onSubmit} noValidate className="flex min-h-0 flex-1 flex-col">
        <div className="min-h-0 flex-1 space-y-5 overflow-y-auto overscroll-contain px-5 py-4">
          <FormAlert>{formError}</FormAlert>
          {guestEverywhere ? (
            <p className="text-sm text-muted-foreground">{t("newGroup.guestOnly")}</p>
          ) : (
            <>
              <TextField
                control={form.control}
                name="name"
                label={t("newGroup.name")}
                description={t("newGroup.nameHint")}
                autoComplete="off"
                autoFocus
              />
              {org ? (
                <OrgSelect
                  label={t("newGroup.org")}
                  hint={t("newGroup.orgHint")}
                  orgs={creatable}
                  value={org.id}
                  onChange={changeOrg}
                />
              ) : null}

              <section aria-labelledby={`${filterId}-label`} className="space-y-3">
                <h3 id={`${filterId}-label`} className="text-sm leading-none font-medium">
                  {t("newGroup.members")}
                </h3>

                {stillRefused.length > 0 ? (
                  <div
                    role="alert"
                    className="space-y-2 rounded-md border border-destructive/30 bg-destructive/8 px-3 py-2.5 text-sm text-destructive"
                  >
                    <p className="font-medium">{t("newGroup.refusedTitle")}</p>
                    <ul className="space-y-1">
                      {stillRefused.map((r) => {
                        const member = byId.get(r.id);
                        return (
                          <li key={r.id}>
                            {member ? (
                              <IntelligenceName intelligence={member.ref} />
                            ) : (
                              <span className="font-medium">{r.id}</span>
                            )}
                            {": "}
                            {refusalText(r.reason)}
                          </li>
                        );
                      })}
                    </ul>
                    <p>{t("newGroup.refusedHint")}</p>
                  </div>
                ) : null}

                {picked.length > 0 ? (
                  <ul
                    aria-label={t("newGroup.selected")}
                    className="flex max-h-24 flex-wrap gap-1.5 overflow-y-auto"
                  >
                    {picked.map((id) => {
                      const p = byId.get(id);
                      if (!p) return null;
                      return (
                        <li
                          key={id}
                          className="flex max-w-full items-center gap-1.5 rounded-full border bg-secondary py-0.5 pr-0.5 pl-1 text-sm"
                        >
                          <IntelligenceAvatar intelligence={p.ref} size="sm" decorative />
                          <IntelligenceName intelligence={p.ref} />
                          <button
                            type="button"
                            aria-label={t("newGroup.remove", { name: p.ref.display_name })}
                            onClick={() => toggle(id, false)}
                            className="grid size-7 shrink-0 place-items-center rounded-full text-muted-foreground outline-none hover:bg-background hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/70"
                          >
                            <XIcon className="size-3.5" aria-hidden="true" />
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                ) : (
                  <p className="text-sm text-muted-foreground">{t("newGroup.selectedNone")}</p>
                )}
                {full ? (
                  <p className="text-sm text-muted-foreground">
                    {t("newGroup.limit", { max: MaxGroupMembers })}
                  </p>
                ) : null}

                {contacts.isPending || orgsLoading || usersLoading ? (
                  <div className="space-y-3 py-1" aria-hidden="true">
                    <Skeleton className="h-4 w-40" />
                    <Skeleton className="h-4 w-56" />
                    <Skeleton className="h-4 w-48" />
                  </div>
                ) : contacts.isError && !contacts.data ? (
                  <div role="alert" className="py-2 text-sm">
                    <p className="text-destructive">{t("contacts.loadError")}</p>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="mt-3"
                      onClick={() => void contacts.refetch()}
                    >
                      {t("common.retry")}
                    </Button>
                  </div>
                ) : total === 0 ? (
                  <p className="py-2 text-sm text-muted-foreground">{t("newGroup.noContacts")}</p>
                ) : (
                  <>
                    <div className="relative">
                      <SearchIcon
                        className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
                        aria-hidden="true"
                      />
                      <Input
                        type="search"
                        value={filter}
                        onChange={(e) => setFilter(e.target.value)}
                        // Enter in the filter must not create the group.
                        onKeyDown={(e) => {
                          if (e.key === "Enter") e.preventDefault();
                        }}
                        placeholder={t("newGroup.filterPlaceholder")}
                        aria-label={t("newGroup.filter")}
                        autoComplete="off"
                        className="pl-9"
                      />
                    </div>
                    {found.length === 0 ? (
                      <p role="status" className="py-2 text-sm text-muted-foreground">
                        {t("newGroup.noMatch")}
                      </p>
                    ) : null}
                    <Candidates
                      label={t("picker.contacts")}
                      items={found}
                      picked={picked}
                      flagged={flagged}
                      full={full}
                      online={(p) =>
                        p.ai ? withContactPresence(p.ai, presence[p.ref.id]).online : false
                      }
                      onToggle={toggle}
                    />
                  </>
                )}
              </section>
            </>
          )}
        </div>

        <DialogFooter className="border-t px-5 py-3">
          <Button type="button" variant="outline" onClick={onCancel}>
            {t("common.cancel")}
          </Button>
          <Button type="submit" disabled={create.isPending || guestEverywhere}>
            {create.isPending ? t("newGroup.creating") : t("newGroup.create")}
          </Button>
        </DialogFooter>
      </form>
    </Form>
  );
}

function Candidates({
  label,
  items,
  picked,
  flagged,
  full,
  online,
  onToggle,
}: {
  label: string;
  items: Pickable[];
  picked: string[];
  flagged: Set<string>;
  full: boolean;
  online: (p: Pickable) => boolean;
  onToggle: (id: string, on: boolean) => void;
}) {
  const prefix = useId();
  if (items.length === 0) return null;
  return (
    <fieldset className="min-w-0">
      <legend className="sr-only">{label}</legend>
      <ul className="-mx-2">
        {items.map((p) => {
          const on = picked.includes(p.ref.id);
          const boxId = `${prefix}-box-${p.ref.id}`;
          const metaId = `${prefix}-meta-${p.ref.id}`;
          return (
            <li key={p.ref.id}>
              <label
                htmlFor={boxId}
                className="flex cursor-pointer items-start gap-3 rounded-md px-2 py-2 hover:bg-accent/40 has-[:focus-visible]:bg-accent/40 has-[[aria-checked=true]]:bg-tape/40"
              >
                <Checkbox
                  id={boxId}
                  data-member={p.ref.id}
                  checked={on}
                  disabled={full && !on}
                  aria-label={p.ref.display_name}
                  aria-describedby={metaId}
                  aria-invalid={flagged.has(p.ref.id) || undefined}
                  onCheckedChange={(next) => onToggle(p.ref.id, next === true)}
                  className="mt-1.5"
                />
                <IntelligenceAvatar intelligence={p.ref} decorative />
                <span className="min-w-0 flex-1">
                  <IntelligenceName intelligence={p.ref} showHandle />
                  <span
                    id={metaId}
                    className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground"
                  >
                    <PickableMeta pickable={p} online={online(p)} />
                  </span>
                </span>
              </label>
            </li>
          );
        })}
      </ul>
    </fieldset>
  );
}
