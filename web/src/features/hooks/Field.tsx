import { cloneElement, useId, type ReactElement, type ReactNode } from "react";
import { Label } from "@/components/ui/label";

/** FieldControl are the props that tie a control, or a group of them, to its label, hint and error. */
export interface FieldControl {
  id: string;
  "aria-labelledby": string;
  "aria-invalid": true | undefined;
  "aria-describedby": string | undefined;
}

/**
 * Field labels a control and wires its hint and error for assistive
 * technology. A single control is given the props; a function child gets
 * them to place itself, e.g. on a radio group, which a <label> can't name.
 * aside sits beside the label.
 */
export function Field({
  label,
  error,
  hint,
  aside,
  children,
}: {
  label: string;
  error?: string | undefined;
  hint?: ReactNode;
  aside?: ReactNode;
  children: ReactElement<Record<string, unknown>> | ((control: FieldControl) => ReactNode);
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errorId = `${id}-error`;
  const control: FieldControl = {
    id,
    "aria-labelledby": `${id}-label`,
    "aria-invalid": error ? true : undefined,
    "aria-describedby":
      [error ? errorId : null, hint ? hintId : null].filter(Boolean).join(" ") || undefined,
  };
  return (
    <div className="space-y-1.5">
      <div className="flex min-h-5 flex-wrap items-center gap-x-2 gap-y-1">
        <Label id={control["aria-labelledby"]} htmlFor={id}>
          {label}
        </Label>
        {aside}
      </div>
      {typeof children === "function"
        ? children(control)
        : cloneElement(children, {
            id,
            "aria-invalid": control["aria-invalid"],
            "aria-describedby": control["aria-describedby"],
          })}
      {error && (
        <p id={errorId} className="text-xs text-destructive">
          {error}
        </p>
      )}
      {hint && (
        <p id={hintId} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
    </div>
  );
}
