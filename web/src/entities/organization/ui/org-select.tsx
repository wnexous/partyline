import { SelectControl } from "@/shared/ui/molecules/form-fields";
import type { MyOrg } from "../lib/orgs";

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
  return (
    <SelectControl
      label={label}
      description={hint}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    >
      {orgs.map((o) => (
        <option key={o.id} value={o.id}>
          {o.name}
        </option>
      ))}
    </SelectControl>
  );
}
