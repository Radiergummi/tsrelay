import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { cn } from "@/lib/utils";

/**
 * CopyButton copies text to the clipboard; label names what it copies.
 * With onHover it stays hidden until the pointer is over the nearest
 * `group` (or it has focus), except on touch screens, which can't hover.
 */
export function CopyButton({
  text,
  label,
  onHover = false,
  className,
}: {
  text: string;
  label: string;
  onHover?: boolean;
  className?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      aria-label={copied ? "Copied" : `Copy ${label}`}
      title={`Copy ${label}`}
      className={cn(
        "shrink-0 rounded-sm p-1 text-muted-foreground hover:text-foreground",
        onHover &&
          "opacity-0 group-hover:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100",
        className,
      )}
      onClick={() => {
        navigator.clipboard.writeText(text).then(
          () => {
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          },
          () => {},
        );
      }}
    >
      {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
    </button>
  );
}
