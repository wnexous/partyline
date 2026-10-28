import { useId, useState } from "react";
import { toast } from "sonner";
import { useMe, useSetDefaultOrg } from "@/entities/account";
import { defaultOrgIdOf, useMyOrgs } from "@/entities/organization";
import { isApiError } from "@/shared/api/client";
import { useT } from "@/shared/i18n";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { PageSection as Section } from "@/shared/ui/molecules/page-section";
import { Skeleton } from "@/shared/ui/primitives/skeleton";

/**
 * The organization new AIs join when they are started without --org (ORG-04): one of the
 * organizations the account belongs to, guest ones included. Only a member can be chosen; the
 * server says 403 for any other, and the select goes back to what it was.
 */
export function DefaultOrgSection() {
  const t = useT();
  const id = useId();
  const me = useMe().data;
  const mine = useMyOrgs();
  const setDefault = useSetDefaultOrg();
  const [choice, setChoice] = useState<string | null>(null);
  const orgs = mine.data ?? [];

  if (mine.isLoading) {
    return (
      <Section id={`${id}-org`} title={t("account.defaultOrg")}>
        <Skeleton aria-hidden="true" className="h-9 w-full max-w-sm" />
      </Section>
    );
  }
  // Without organizations to choose from there is nothing to set (or they could not be read).
  if (orgs.length === 0) return null;

  // A default the account no longer belongs to counts for nothing: the select asks for a choice.
  const defaultId = defaultOrgIdOf(me);
  const current = orgs.some((o) => o.id === defaultId) ? (defaultId ?? "") : "";
  const value = choice ?? current;

  const choose = (orgId: string) => {
    if (orgId === value) return;
    setChoice(orgId);
    setDefault.mutate(orgId, {
      onSuccess: () =>
        toast.success(
          t("account.defaultOrgSaved", { org: orgs.find((o) => o.id === orgId)?.name ?? "" }),
        ),
      onError: (err) =>
        toast.error(
          isApiError(err) && err.status === 403
            ? t("account.defaultOrgForbidden")
            : t("account.defaultOrgFailed"),
        ),
      onSettled: () => setChoice(null),
    });
  };

  return (
    <Section
      id={`${id}-org`}
      title={t("account.defaultOrg")}
      description={t("account.defaultOrgBody")}
    >
      <NativeSelect
        aria-label={t("account.defaultOrgLabel")}
        value={value}
        disabled={setDefault.isPending}
        onChange={(e) => choose(e.target.value)}
        className="max-w-sm"
      >
        {value === "" ? (
          <option value="" disabled>
            {t("account.defaultOrgNone")}
          </option>
        ) : null}
        {orgs.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </NativeSelect>
    </Section>
  );
}
