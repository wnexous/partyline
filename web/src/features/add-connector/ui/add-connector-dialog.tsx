import { useNavigate } from "@tanstack/react-router";
import { useEffect, useId, useState } from "react";
import { useConnectorKinds, useCreateConnector } from "@/entities/connector";
import { isAdminOf, OrgSelect, useCurrentOrg } from "@/entities/organization";
import { isApiError } from "@/shared/api/client";
import {
  type Connector,
  type ConnectorKind,
  HeaderSignature,
  HeaderTimestamp,
} from "@/shared/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { cn } from "@/shared/lib/utils";
import { CodeBlock } from "@/shared/ui/molecules/copy-field";
import { FormDialog } from "@/shared/ui/molecules/form-dialog";
import { FormAlert } from "@/shared/ui/molecules/form-fields";
import { SecretOnceDialog } from "@/shared/ui/molecules/secret-once-dialog";
import { Form } from "@/shared/ui/primitives/form";
import { RadioGroup, RadioGroupItem } from "@/shared/ui/primitives/radio-group";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { ConnectorFields, useConnectorForm } from "./connector-form";

/** Secrets the server generated for a new connector, with the labels of their fields. */
interface Generated {
  connector: Connector;
  secrets: { label: string; value: string }[];
}

/** How a sender signs a webhook request (HOOK-04): Unix seconds, HMAC over "timestamp.body". */
const SIGNING_RECIPE = [
  `${HeaderTimestamp}: <unix_seconds>`,
  `${HeaderSignature}: sha256=<hex(HMAC_SHA256(secret, timestamp + "." + body))>`,
].join("\n");

/**
 * Adds a connector (UI-16): pick the organization it belongs to (one the viewer administers,
 * ORG-03), pick a platform, fill the form its field schema describes. Secrets the server
 * generates (the webhook's signing secret) are shown once in the secret dialog (UI-19); the new
 * connector's page opens after it closes.
 */
export function AddConnectorDialog({
  open,
  onOpenChange,
  orgId,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The organization the page is about: the dialog opens on it (SET-08). */
  orgId: string;
}) {
  const t = useT();
  const navigate = useNavigate();
  const [generated, setGenerated] = useState<Generated | null>(null);
  const first = generated?.secrets[0];
  // The new connection opens under the organization that owns it (SET-08), whatever the dialog
  // was set to: the list it came from filters on the same `org_id`.
  const openConnector = (id: string, destinationOrgId: string) =>
    void navigate({
      to: "/orgs/$orgId/connections/$connectorId",
      params: { orgId: destinationOrgId, connectorId: id },
    });

  return (
    <>
      <AddConnectorForm
        open={open}
        defaultOrgId={orgId}
        onCancel={() => onOpenChange(false)}
        onCreated={(connector, kind) => {
          onOpenChange(false);
          const secrets = Object.entries(connector.generated_secrets ?? {}).map(
            ([name, value]) => ({
              label: kind.fields.find((f) => f.name === name)?.label ?? name,
              value,
            }),
          );
          if (secrets.length > 0) setGenerated({ connector, secrets });
          else openConnector(connector.id, connector.org_id);
        }}
      />
      <SecretOnceDialog
        secret={first?.value ?? null}
        title={t("connectors.secretTitle")}
        description={t("connectors.secretBody", { name: generated?.connector.name ?? "" })}
        label={first?.label ?? ""}
        warning={t("connectors.secretWarning")}
        confirmLabel={t("connectors.secretDone")}
        onClose={() => {
          const connector = generated?.connector;
          setGenerated(null);
          if (connector) openConnector(connector.id, connector.org_id);
        }}
      >
        {generated?.secrets.slice(1).map((s) => (
          <CodeBlock key={s.label} label={s.label} value={s.value} />
        ))}
        {generated?.connector.inbound_url ? (
          <>
            <CodeBlock label={t("connector.inboundUrl")} value={generated.connector.inbound_url} />
            <CodeBlock label={t("connectors.signing")} value={SIGNING_RECIPE} />
          </>
        ) : null}
      </SecretOnceDialog>
    </>
  );
}

/** What the dialog says when the server turns the connection down. */
function failureText(t: Translate, err: unknown): string {
  if (!isApiError(err)) return t("error.generic");
  if (err.status === 0) return t("error.network");
  // Only the admins of an organization add its connections (ORG-03).
  if (err.status === 403) return t("connectors.addForbidden");
  return t("error.generic");
}

/**
 * The dialog's body and its form: the organization the connection goes to, the platform, and the
 * fields that platform's schema describes. It stays mounted between openings (the dialog animates
 * closed) and every opening starts from the first platform and the page's organization.
 */
function AddConnectorForm({
  open,
  defaultOrgId,
  onCancel,
  onCreated,
}: {
  open: boolean;
  defaultOrgId: string;
  onCancel: () => void;
  onCreated: (connector: Connector, kind: ConnectorKind) => void;
}) {
  const t = useT();
  const kinds = useConnectorKinds(open);
  const create = useCreateConnector();
  const { org: current, orgs } = useCurrentOrg();
  const [chosen, setChosen] = useState<string | null>(null);
  const [pickedOrg, setPickedOrg] = useState<string | null>(null);
  const idBase = useId();
  const kindName = chosen ?? (kinds.data?.length === 1 ? kinds.data[0]?.name : null) ?? null;
  const kind = kinds.data?.find((k) => k.name === kindName);
  const { form, formError, handleSubmit, reset } = useConnectorForm(kind);

  // A new opening is a new connection: the platform, the organization and the fields start over.
  // biome-ignore lint/correctness/useExhaustiveDependencies: only the opening resets the form
  useEffect(() => {
    if (!open) return;
    setChosen(null);
    setPickedOrg(null);
    reset();
  }, [open]);

  // The organization it goes to: the pick, else the one the page is about when the viewer
  // administers it (SET-08: the address wins over the browser's current organization), else the
  // current one they administer, else the first they administer. None while their organizations
  // are unknown: the server then uses their default and decides whether they may.
  const adminOrgs = orgs.filter(isAdminOf);
  const org =
    adminOrgs.find((o) => o.id === pickedOrg) ??
    adminOrgs.find((o) => o.id === defaultOrgId) ??
    adminOrgs.find((o) => o.id === current?.id) ??
    adminOrgs[0];

  return (
    <Form {...form}>
      <FormDialog
        open={open}
        onOpenChange={(next) => {
          if (!next) onCancel();
        }}
        title={t("connectors.addTitle")}
        description={t("connectors.addBody")}
        onSubmit={handleSubmit((values, api) => {
          if (!kind) return;
          create.mutate(
            { kind: kind.name, ...values, ...(org ? { org_id: org.id } : {}) },
            {
              onSuccess: (c) => onCreated(c, kind),
              onError: (err) => {
                if (isApiError(err) && err.status === 422 && api.applyServerErrors(err.fields)) {
                  return;
                }
                api.setFormError(failureText(t, err));
              },
            },
          );
        })}
        submitLabel={t("connectors.add")}
        submitDisabled={!kind}
        pending={create.isPending}
        contentClassName="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 sm:max-w-lg"
        headerClassName="border-b px-5 pt-5 pb-4 text-left"
        formClassName="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 pt-4 pb-5"
      >
        {org ? (
          <OrgSelect
            label={t("connectors.org")}
            hint={t("connectors.orgHint")}
            orgs={adminOrgs}
            value={org.id}
            onChange={setPickedOrg}
          />
        ) : null}
        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">{t("connectors.platform")}</legend>
          {kinds.isPending ? (
            <Skeleton className="h-10 w-full" />
          ) : kinds.isError ? (
            <p role="alert" className="text-sm text-destructive">
              {t("connectors.kindsError")}
            </p>
          ) : (
            <RadioGroup
              value={kindName ?? ""}
              onValueChange={setChosen}
              className="grid grid-cols-2 gap-2 sm:grid-cols-4"
            >
              {kinds.data.map((k) => (
                <label
                  key={k.name}
                  htmlFor={`${idBase}-${k.name}`}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2.5 text-sm font-medium transition-colors hover:bg-accent/60",
                    k.name === kindName && "border-tape-strong bg-tape/40",
                  )}
                >
                  <RadioGroupItem id={`${idBase}-${k.name}`} value={k.name} />
                  {k.label}
                </label>
              ))}
            </RadioGroup>
          )}
        </fieldset>
        {kind ? (
          <>
            <FormAlert>{formError}</FormAlert>
            <ConnectorFields kind={kind} form={form} />
          </>
        ) : null}
      </FormDialog>
    </Form>
  );
}
