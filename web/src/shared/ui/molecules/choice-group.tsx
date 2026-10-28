import type { ReactNode } from "react";
import { cn } from "@/shared/lib/utils";
import { RadioGroup, RadioGroupItem } from "@/shared/ui/primitives/radio-group";

export interface ChoiceOption {
  value: string;
  /** What the card says: its name, as text or as elements. */
  label: ReactNode;
  /** A glyph shown before the label (decorative: the label is the name). */
  icon?: ReactNode;
  /** The language of the label, when it is not the page's ("Português (Brasil)"). */
  lang?: string;
}

/*
 * Pick one of a few, as cards: a legend, then one bordered card per option with its radio. The card
 * is the radio's label, so the whole card is the target. Each card's id is `${idBase}-${value}`.
 * `className` lays the cards out (a grid); `itemClassName` adjusts one card's spacing.
 * `emphasizeChecked` makes the chosen card's text medium weight.
 */
export function ChoiceGroup({
  legend,
  value,
  onValueChange,
  options,
  idBase,
  disabled,
  className,
  fieldsetClassName,
  itemClassName,
  emphasizeChecked = false,
  children,
}: {
  legend: ReactNode;
  value: string;
  onValueChange: (value: string) => void;
  options: readonly ChoiceOption[];
  idBase: string;
  disabled?: boolean;
  className?: string;
  fieldsetClassName?: string;
  itemClassName?: string;
  emphasizeChecked?: boolean;
  /** Anything more inside the fieldset, after the cards (an error, a hint). */
  children?: ReactNode;
}) {
  return (
    <fieldset className={fieldsetClassName}>
      <legend className="mb-2 text-sm font-medium">{legend}</legend>
      <RadioGroup
        value={value}
        onValueChange={onValueChange}
        disabled={disabled}
        className={cn("grid gap-2", className)}
      >
        {options.map((option) => {
          const id = `${idBase}-${option.value}`;
          const checked = option.value === value;
          return (
            <label
              key={option.value}
              htmlFor={id}
              lang={option.lang}
              className={cn(
                "flex cursor-pointer items-center gap-2 rounded-md border px-3 py-2 text-sm transition-colors hover:bg-accent/60",
                checked && "border-tape-strong bg-tape/40",
                checked && emphasizeChecked && "font-medium",
                itemClassName,
              )}
            >
              <RadioGroupItem id={id} value={option.value} />
              {option.icon}
              {option.label}
            </label>
          );
        })}
      </RadioGroup>
      {children}
    </fieldset>
  );
}
