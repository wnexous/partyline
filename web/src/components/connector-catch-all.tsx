import { Link } from "@tanstack/react-router";
import { TriangleAlertIcon } from "lucide-react";
import { RadioGroup as RadioGroupPrimitive } from "radix-ui";
import { useId, useState } from "react";
import { toast } from "sonner";
import { FormAlert } from "@/components/form-fields";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isApiError } from "@/lib/api/client";
import { useRooms, useUpdateConnector } from "@/lib/api/queries";
import {
  type AutoRouteScope,
  AutoRouteScopeAll,
  AutoRouteScopeDMs,
  AutoRouteScopeGroups,
  AutoRouteScopeOff,
  type Connector,
  LevelManage,
} from "@/lib/api/types.gen";
import { type MessageKey, useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

/*
 * Catch-all routing for a connector (ROUTE-01, ROUTE-04): new chats of the chosen kind that are in
 * no room join one room the user manages. Anyone who writes then reaches that room's agents, so
 * the exposure is spelled out whenever the scope is on.
 */

const SCOPES: { scope: AutoRouteScope; label: MessageKey }[] = [
  { scope: AutoRouteScopeOff, label: "catchAll.scope.off" },
  { scope: AutoRouteScopeDMs, label: "catchAll.scope.dms" },
  { scope: AutoRouteScopeGroups, label: "catchAll.scope.groups" },
  { scope: AutoRouteScopeAll, label: "catchAll.scope.all" },
];

const selectClass =
  "h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-destructive/20 dark:bg-input/30";

export function ConnectorCatchAll({ connector: c }: { connector: Connector }) {
  const t = useT();
  const rooms = useRooms();
  const update = useUpdateConnector(c.id);
  const [scope, setScope] = useState<AutoRouteScope>(c.auto_route_scope);
  const [roomId, setRoomId] = useState(c.auto_route_room_id ?? "");
  const [roomError, setRoomError] = useState<string | null>(null);
  const [scopeError, setScopeError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const titleId = useId();
  const roomFieldId = useId();
  const on = scope !== AutoRouteScopeOff;

  const managed = (rooms.data ?? []).filter((r) => r.my_level === LevelManage);
  const roomName = managed.find((r) => r.id === roomId)?.name;
  const unknownRoom = roomId !== "" && rooms.data !== undefined && !roomName;
  const dirty = scope !== c.auto_route_scope || roomId !== (c.auto_route_room_id ?? "");

  const save = () => {
    setFormError(null);
    setRoomError(null);
    setScopeError(null);
    if (on && roomId === "") {
      setRoomError(t("catchAll.roomRequired"));
      return;
    }
    update.mutate(
      { auto_route_scope: scope, auto_route_room_id: on ? roomId : "" },
      {
        onSuccess: (saved) => {
          setRoomId(saved.auto_route_room_id ?? "");
          toast.success(t("catchAll.saved"));
        },
        onError: (err) => {
          if (isApiError(err) && err.status === 422) {
            const { auto_route_room_id: room, auto_route_scope: sc } = err.fields;
            if (room) setRoomError(room);
            if (sc) setScopeError(sc);
            if (room || sc) return;
          }
          setFormError(
            isApiError(err) && err.status === 403 ? t("catchAll.forbidden") : t("error.generic"),
          );
        },
      },
    );
  };

  return (
    <section aria-labelledby={titleId} className="space-y-3">
      <h2 id={titleId} className="label-caps text-[0.72rem] text-muted-foreground">
        {t("catchAll.title")}
      </h2>
      <div className="space-y-4 rounded-md border px-4 py-4">
        <p className="text-sm text-muted-foreground">{t("catchAll.body")}</p>
        <FormAlert>{formError}</FormAlert>

        <div className="space-y-2">
          <p id={`${titleId}-scope`} className="text-sm font-medium">
            {t("catchAll.scope")}
          </p>
          <RadioGroupPrimitive.Root
            value={scope}
            onValueChange={(v) => {
              setScope(v as AutoRouteScope);
              setScopeError(null);
            }}
            aria-labelledby={`${titleId}-scope`}
            aria-invalid={scopeError ? true : undefined}
            className="grid grid-cols-2 gap-1 rounded-md bg-muted p-1 sm:grid-cols-4"
          >
            {SCOPES.map((s) => (
              <RadioGroupPrimitive.Item
                key={s.scope}
                value={s.scope}
                className={cn(
                  "h-8 rounded-sm px-2 text-sm text-muted-foreground transition-[color,background-color,box-shadow] outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50",
                  "data-[state=checked]:bg-background data-[state=checked]:font-medium data-[state=checked]:text-foreground data-[state=checked]:shadow-sm",
                  s.scope !== AutoRouteScopeOff &&
                    "data-[state=checked]:bg-tape data-[state=checked]:text-tape-foreground",
                )}
              >
                {t(s.label)}
              </RadioGroupPrimitive.Item>
            ))}
          </RadioGroupPrimitive.Root>
          {scopeError ? <p className="text-sm text-destructive">{scopeError}</p> : null}
        </div>

        {on ? (
          <div className="space-y-2">
            <label htmlFor={roomFieldId} className="block text-sm font-medium">
              {t("catchAll.room")}
            </label>
            {rooms.isPending ? (
              <Skeleton className="h-9 w-full" />
            ) : rooms.isError ? (
              <p className="text-sm text-destructive">{t("catchAll.roomsFailed")}</p>
            ) : managed.length === 0 && !unknownRoom ? (
              <p className="text-sm text-muted-foreground">
                {t("catchAll.noRooms")}{" "}
                <Link
                  to="/rooms"
                  className="font-medium text-foreground underline-offset-4 hover:underline"
                >
                  {t("nav.rooms")}
                </Link>
              </p>
            ) : (
              <>
                <select
                  id={roomFieldId}
                  value={roomId}
                  onChange={(e) => {
                    setRoomId(e.target.value);
                    setRoomError(null);
                  }}
                  aria-invalid={roomError ? true : undefined}
                  aria-describedby={roomError ? `${roomFieldId}-error` : `${roomFieldId}-hint`}
                  className={selectClass}
                >
                  <option value="">{t("catchAll.roomPlaceholder")}</option>
                  {unknownRoom ? (
                    <option value={roomId}>{t("catchAll.roomUnavailable")}</option>
                  ) : null}
                  {managed.map((r) => (
                    <option key={r.id} value={r.id}>
                      {r.name}
                    </option>
                  ))}
                </select>
                {roomError ? (
                  <p id={`${roomFieldId}-error`} className="text-sm text-destructive">
                    {roomError}
                  </p>
                ) : (
                  <p id={`${roomFieldId}-hint`} className="text-xs text-muted-foreground">
                    {t("catchAll.roomHint")}
                  </p>
                )}
              </>
            )}
          </div>
        ) : null}

        {on ? (
          <div
            role="note"
            className="flex gap-2.5 rounded-md border border-warning/40 bg-warning/12 px-3 py-2.5 text-sm"
          >
            <TriangleAlertIcon className="mt-0.5 size-4 shrink-0 text-warning" aria-hidden="true" />
            <p className="min-w-0">
              <span className="font-medium">
                {roomName ? t("catchAll.warning", { room: roomName }) : t("catchAll.warningNoRoom")}
              </span>{" "}
              <span className="text-foreground/80">{t("catchAll.warningTrust")}</span>
            </p>
          </div>
        ) : null}

        <div className="flex justify-end">
          <Button
            size="sm"
            disabled={!dirty || update.isPending}
            aria-label={t("catchAll.save")}
            onClick={save}
          >
            {t("common.save")}
          </Button>
        </div>
      </div>
    </section>
  );
}
