import { useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import { ConnectorForm } from "@/components/schema-form";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import { useConnectorKinds, useCreateConnector } from "@/lib/api/queries";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/** Adds a connector (UI-16): pick a platform, fill the form its field schema describes. */
export function AddConnectorDialog({
  open,
  onOpenChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useT();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[calc(100dvh-2rem)] flex-col gap-0 p-0 sm:max-w-lg">
        <DialogHeader className="border-b px-5 pt-5 pb-4 text-left">
          <DialogTitle>{t("connectors.addTitle")}</DialogTitle>
          <DialogDescription>{t("connectors.addBody")}</DialogDescription>
        </DialogHeader>
        {open ? <AddConnectorBody onDone={() => onOpenChange(false)} /> : null}
      </DialogContent>
    </Dialog>
  );
}

function AddConnectorBody({ onDone }: { onDone: () => void }) {
  const t = useT();
  const navigate = useNavigate();
  const kinds = useConnectorKinds();
  const create = useCreateConnector();
  const [chosen, setChosen] = useState<string | null>(null);
  const idBase = useId();
  const kindName = chosen ?? (kinds.data?.length === 1 ? kinds.data[0]?.name : null) ?? null;
  const kind = kinds.data?.find((k) => k.name === kindName);

  return (
    <div className="min-h-0 flex-1 space-y-5 overflow-y-auto px-5 pt-4 pb-5">
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
                  k.name === kindName && "border-tape-strong bg-tape/35 dark:bg-tape/12",
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
          onCancel={onDone}
          onSubmit={(values, form) =>
            create.mutate(
              { kind: kind.name, ...values },
              {
                onSuccess: (c) => {
                  onDone();
                  void navigate({ to: "/connectors/$connectorId", params: { connectorId: c.id } });
                },
                onError: (err) => {
                  if (isApiError(err) && err.status === 422 && form.applyServerErrors(err.fields)) {
                    return;
                  }
                  form.setFormError(
                    isApiError(err) && err.status === 0 ? t("error.network") : t("error.generic"),
                  );
                },
              },
            )
          }
        />
      ) : null}
    </div>
  );
}
