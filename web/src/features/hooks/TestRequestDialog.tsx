import { useState } from "react";
import { ApiError } from "@/api/client";
import { useTestRequest } from "@/api/mutations";
import type { FieldError, Hook } from "@/api/schemas";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { outcomeLabel } from "@/lib/format";
import { toast } from "@/lib/toast";
import { Field } from "./Field";
import { parseHeaders } from "./formRequest";

/** TestRequestDialog sends a request through the normal pipeline, as a call of kind test (DESIGN.md §9.3). */
export function TestRequestDialog({
  hook,
  open,
  onOpenChange,
  onSent,
}: {
  hook: Hook;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSent: (callId: string) => void;
}) {
  const [method, setMethod] = useState("POST");
  const [path, setPath] = useState("/");
  const [headers, setHeaders] = useState("");
  const [body, setBody] = useState("");
  const [errors, setErrors] = useState<FieldError[]>([]);
  const test = useTestRequest(hook.id);
  const err = (f: string) => errors.find((e) => e.field === f)?.message;
  const headerErr =
    errors
      .filter((e) => e.field.startsWith("headers."))
      .map((e) => `${e.field.slice(8)} ${e.message}`)
      .join("; ") || undefined;

  const send = (e: React.FormEvent) => {
    e.preventDefault();
    test.mutate(
      { method, path, headers: parseHeaders(headers), body },
      {
        onSuccess: (c) => {
          setErrors([]);
          onOpenChange(false);
          toast.success(`Sent: ${outcomeLabel(c)}`);
          onSent(c.id);
        },
        onError: (x) => setErrors(x instanceof ApiError ? x.fieldErrors : []),
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Send test request</DialogTitle>
          <DialogDescription>
            It goes through the relay to the attached tunnel like a real callback, and appears in
            the calls as a test.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={send} className="space-y-3" noValidate>
          <div className="grid grid-cols-[7rem_1fr] gap-3">
            <Field label="Method" error={err("method")}>
              <Input value={method} onChange={(e) => setMethod(e.target.value.toUpperCase())} />
            </Field>
            <Field
              label="Path"
              error={err("path")}
              hint="Below the hook's URL; a query string is allowed."
            >
              <Input value={path} onChange={(e) => setPath(e.target.value)} />
            </Field>
          </div>
          <Field label="Headers" error={headerErr} hint="One Name: value per line.">
            <Textarea
              value={headers}
              onChange={(e) => setHeaders(e.target.value)}
              rows={3}
              placeholder="Content-Type: application/json"
              className="font-mono text-xs"
            />
          </Field>
          <Field label="Body">
            <Textarea
              value={body}
              onChange={(e) => setBody(e.target.value)}
              rows={5}
              className="font-mono text-xs"
            />
          </Field>
          <DialogFooter>
            <Button type="submit" disabled={test.isPending}>
              Send
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
