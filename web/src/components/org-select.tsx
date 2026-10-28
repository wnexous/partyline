import { useId } from "react";
import type { MyOrg } from "@/lib/orgs";
import { NativeSelect } from "@/shared/ui/atoms/native-select";
import { Label } from "@/shared/ui/primitives/label";

/**
 * The organization a new resource goes to (ORG-03): the ones the viewer may add to, and a line on
 * what the choice means. A native select, so the phone's own picker opens (UI-24).
 */
export function OrgSelect({
  label,
  hint,
  orgs,
  value,
  onChange,
}: {
  label: string;
  hint: string;
  orgs: readonly MyOrg[];
  value: string;
  onChange: (orgId: string) => void;
}) {
  const id = useId();
  return (
    <div className="grid gap-2">
      <Label htmlFor={`${id}-org`}>{label}</Label>
      <NativeSelect
        id={`${id}-org`}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-describedby={`${id}-hint`}
      >
        {orgs.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </NativeSelect>
      <p id={`${id}-hint`} className="text-xs text-muted-foreground">
        {hint}
      </p>
    </div>
  );
}
