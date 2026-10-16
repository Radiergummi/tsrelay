import { useLicenseText } from "@/api/queries";
import type { LicenseComponent } from "@/api/schemas";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { licenseLabel } from "@/lib/licenseLabel";

function TextPane({ id, enabled, failed }: { id: string; enabled: boolean; failed: string }) {
  const text = useLicenseText(id, enabled);
  if (text.isError) return <p className="text-sm text-destructive">{failed}</p>;
  return (
    <pre className="font-mono text-xs whitespace-pre-wrap text-muted-foreground">
      {text.data ?? "Loading…"}
    </pre>
  );
}

/**
 * LicenseTextDialog shows a component's license text, and its NOTICE when
 * it ships one. The texts are fetched when it opens, not with the page:
 * most visitors open none of them.
 */
export function LicenseTextDialog({
  component,
  open,
  onOpenChange,
}: {
  component: LicenseComponent;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { textId, noticeId } = component;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{component.name}</DialogTitle>
          <DialogDescription>
            {component.licenses.map(licenseLabel).join(", ")}
            {component.version ? ` · ${component.version}` : ""}
          </DialogDescription>
        </DialogHeader>
        {textId && (
          <TextPane id={textId} enabled={open} failed="Could not load the license text." />
        )}
        {noticeId && (
          // A NOTICE is not part of the license, so it must not read as though it were.
          <section className="border-t pt-4">
            <h3 className="mb-2 text-sm font-medium">NOTICE</h3>
            <TextPane id={noticeId} enabled={open} failed="Could not load the notice." />
          </section>
        )}
      </DialogContent>
    </Dialog>
  );
}
