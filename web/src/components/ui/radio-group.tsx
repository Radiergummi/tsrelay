import { Radio } from "@base-ui/react/radio";
import { RadioGroup as RadioGroupPrimitive } from "@base-ui/react/radio-group";
import { cn } from "@/lib/utils";

// Each radio here is a Base UI radio: role="radio", named by its content,
// and the group moves between them with the arrow keys.

const focus = "outline-none focus-visible:ring-3 focus-visible:ring-ring/50";

/** chip is RadioChip's pill shape, for buttons that sit among chips. */
const chip = cn(
  "inline-flex h-7 items-center rounded-full border border-input px-2.5 text-sm hover:bg-muted",
  focus,
);

function RadioGroup({ className, ...props }: RadioGroupPrimitive.Props<string>) {
  return (
    <RadioGroupPrimitive
      data-slot="radio-group"
      className={cn("flex flex-wrap gap-1.5", className)}
      {...props}
    />
  );
}

/** SegmentedControl is a radio group drawn as one row of joined segments. */
function SegmentedControl({ className, ...props }: RadioGroupPrimitive.Props<string>) {
  return (
    <RadioGroupPrimitive
      data-slot="segmented-control"
      className={cn(
        "inline-flex max-w-full flex-wrap gap-0.5 rounded-lg border border-input p-0.5 aria-invalid:border-destructive",
        className,
      )}
      {...props}
    />
  );
}

function Segment({ className, ...props }: Radio.Root.Props) {
  return (
    <Radio.Root
      data-slot="segment"
      className={cn(
        "inline-flex h-7 cursor-default items-center rounded-md px-2.5 text-sm whitespace-nowrap text-muted-foreground select-none hover:text-foreground data-checked:bg-primary data-checked:text-primary-foreground",
        focus,
        className,
      )}
      {...props}
    />
  );
}

/**
 * RadioChip is a pill-shaped radio, for presets. inherited draws a
 * checked chip as standing in for a default rather than as a choice.
 */
function RadioChip({ className, inherited, ...props }: Radio.Root.Props & { inherited?: boolean }) {
  return (
    <Radio.Root
      data-slot="radio-chip"
      data-inherited={inherited || undefined}
      className={cn(
        chip,
        "cursor-default gap-1 whitespace-nowrap select-none data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground data-inherited:data-checked:border-dashed data-inherited:data-checked:border-foreground/40 data-inherited:data-checked:bg-muted data-inherited:data-checked:text-foreground",
        className,
      )}
      {...props}
    />
  );
}

/** RadioCard is a radio with room for a title and an explanation. */
function RadioCard({ className, ...props }: Radio.Root.Props) {
  return (
    <Radio.Root
      data-slot="radio-card"
      className={cn(
        "flex cursor-default flex-col items-start gap-0.5 rounded-lg border border-input p-2.5 text-left text-sm select-none hover:bg-muted/50 data-checked:border-primary data-checked:ring-1 data-checked:ring-primary",
        focus,
        className,
      )}
      {...props}
    />
  );
}

export { chip, RadioCard, RadioChip, RadioGroup, Segment, SegmentedControl };
