import type { ComponentType } from "react";
import { useLicenseText } from "@/api/queries";
import type { LicenseComponent } from "@/api/schemas";
import { QueryView } from "@/components/QueryView";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { licenseLabel, licensesOf } from "@/lib/licenseLabel";

const textClass = "font-mono text-xs whitespace-pre-wrap text-muted-foreground";

const failedClass = "text-sm text-destructive";
const TextFailed = () => <p className={failedClass}>Could not load the license text.</p>;
const NoticeFailed = () => <p className={failedClass}>Could not load the notice.</p>;

function TextPane({
  id,
  enabled,
  failed,
}: {
  id: string;
  enabled: boolean;
  failed: ComponentType<{ error: unknown }>;
}) {
  const text = useLicenseText(id, enabled);
  return (
    <QueryView query={text} pending={<pre className={textClass}>Loading…</pre>} error={failed}>
      {(body) => <pre className={textClass}>{body}</pre>}
    </QueryView>
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
            {licensesOf(component).map(licenseLabel).join(", ")}
            {component.version ? ` · ${component.version}` : ""}
          </DialogDescription>
        </DialogHeader>
        {textId && <TextPane id={textId} enabled={open} failed={TextFailed} />}
        {noticeId && (
          // A NOTICE is not part of the license, so it must not read as though it were.
          <section className="border-t pt-4">
            <h3 className="mb-2 text-sm font-medium">NOTICE</h3>
            <TextPane id={noticeId} enabled={open} failed={NoticeFailed} />
          </section>
        )}
      </DialogContent>
    </Dialog>
  );
}
