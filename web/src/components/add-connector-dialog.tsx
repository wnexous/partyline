import { useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import { CodeBlock } from "@/shared/ui/molecules/copy-field";
import { OrgSelect } from "@/components/org-select";
import { ConnectorForm } from "@/components/schema-form";
import { SecretOnceDialog } from "@/components/secret-once-dialog";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/shared/ui/primitives/dialog";
import { RadioGroup, RadioGroupItem } from "@/shared/ui/primitives/radio-group";
import { Skeleton } from "@/shared/ui/primitives/skeleton";
import { useCurrentOrg } from "@/hooks/use-current-org";
import { isApiError } from "@/lib/api/client";
import { useConnectorKinds, useCreateConnector } from "@/lib/api/queries";
import {
  type Connector,
  type ConnectorKind,
  HeaderSignature,
  HeaderTimestamp,
} from "@/lib/api/types.gen";
import { type Translate, useT } from "@/shared/i18n";
import { isAdminOf } from "@/lib/orgs";
import { cn } from "@/shared/lib/utils";
import { isCoarsePointer } from "@/shared/lib/device";

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
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 sm:max-w-lg" closeLabel={t("common.close")} touchFirst={isCoarsePointer()}>
          <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
            <DialogTitle>{t("connectors.addTitle")}</DialogTitle>
            <DialogDescription>{t("connectors.addBody")}</DialogDescription>
          </DialogHeader>
          {open ? (
            <AddConnectorBody
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
          ) : null}
        </DialogContent>
      </Dialog>
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

function AddConnectorBody({
  defaultOrgId,
  onCancel,
  onCreated,
}: {
  defaultOrgId: string;
  onCancel: () => void;
  onCreated: (connector: Connector, kind: ConnectorKind) => void;
}) {
  const t = useT();
  const kinds = useConnectorKinds();
  const create = useCreateConnector();
  const { org: current, orgs } = useCurrentOrg();
  const [chosen, setChosen] = useState<string | null>(null);
  const [pickedOrg, setPickedOrg] = useState<string | null>(null);
  const idBase = useId();
  const kindName = chosen ?? (kinds.data?.length === 1 ? kinds.data[0]?.name : null) ?? null;
  const kind = kinds.data?.find((k) => k.name === kindName);
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
    <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 pt-4 pb-5">
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
        <ConnectorForm
          key={kind.name}
          kind={kind}
          submitLabel={t("connectors.add")}
          pending={create.isPending}
          onCancel={onCancel}
          onSubmit={(values, form) =>
            create.mutate(
              { kind: kind.name, ...values, ...(org ? { org_id: org.id } : {}) },
              {
                onSuccess: (c) => onCreated(c, kind),
                onError: (err) => {
                  if (isApiError(err) && err.status === 422 && form.applyServerErrors(err.fields)) {
                    return;
                  }
                  form.setFormError(failureText(t, err));
                },
              },
            )
          }
        />
      ) : null}
    </div>
  );
}
