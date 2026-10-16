import { CopyButton } from "@/components/ui/copy-button";
import { installCommand, type Platform } from "@/lib/install";

/** InstallCommand shows the one-liner that installs the CLI from the relay at origin (this page's by default). */
export function InstallCommand({
  platform,
  origin = window.location.origin,
}: {
  platform: Platform;
  origin?: string;
}) {
  const command = installCommand(platform, origin);
  return (
    <div className="flex items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 text-left">
      <code className="min-w-0 flex-1 overflow-x-auto text-xs whitespace-nowrap">{command}</code>
      <CopyButton text={command} label="install command" />
    </div>
  );
}
