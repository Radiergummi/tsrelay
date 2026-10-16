import { Autocomplete } from "@base-ui/react/autocomplete";
import type { ComponentProps } from "react";
import { Input } from "@/components/ui/input";

/**
 * Combobox is a text input that suggests items as you type, and accepts
 * any text too. It follows the ARIA combobox pattern: the arrow keys move
 * through the suggestions, Enter takes one and Escape closes them.
 */
function Combobox({
  items,
  value,
  onValueChange,
  ...props
}: {
  items: string[];
  value: string;
  onValueChange: (value: string) => void;
} & Omit<ComponentProps<"input">, "value" | "onChange" | "defaultValue">) {
  return (
    <Autocomplete.Root
      items={items}
      value={value}
      onValueChange={(v) => onValueChange(v)}
      openOnInputClick
    >
      <Autocomplete.Input data-slot="combobox" render={<Input />} {...props} />
      <Autocomplete.Portal>
        <Autocomplete.Positioner sideOffset={4} className="z-50 outline-none">
          <Autocomplete.Popup className="w-(--anchor-width) max-w-(--available-width) rounded-lg bg-popover text-sm text-popover-foreground shadow-md ring-1 ring-border">
            <Autocomplete.List className="max-h-[min(15rem,var(--available-height))] overflow-y-auto p-1 outline-none data-empty:p-0">
              {(item: string) => (
                <Autocomplete.Item
                  key={item}
                  value={item}
                  className="cursor-default rounded-md px-2 py-1 select-none data-highlighted:bg-muted"
                >
                  {item}
                </Autocomplete.Item>
              )}
            </Autocomplete.List>
          </Autocomplete.Popup>
        </Autocomplete.Positioner>
      </Autocomplete.Portal>
    </Autocomplete.Root>
  );
}

export { Combobox };
