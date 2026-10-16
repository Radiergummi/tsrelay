import { ChevronRight } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError, fieldMessage, formMessage } from "@/api/client";
import { dryRunHook, useCreateHook, useUpdateHook } from "@/api/mutations";
import type { FieldError, Hook, Me } from "@/api/schemas";
import { Button, buttonVariants } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { channelChoices } from "@/lib/channels";
import { formatSize } from "@/lib/format";
import { toast } from "@/lib/toast";
import { Field } from "./Field";
import { fieldTypes, formRequest, initialValues } from "./formRequest";

const dryRunDelay = 400;

/** HookForm creates a hook, or edits hook. Every change is validated by a dry run. */
export function HookForm({ me, hook }: { me: Me; hook?: Hook }) {
  const editing = hook !== undefined;
  const [values, setValues] = useState(() => initialValues(hook));
  const [errors, setErrors] = useState<FieldError[]>([]);
  const [formError, setFormError] = useState("");
  // Delivery starts shut unless the hook overrides a default, and opens
  // (and stays open) when one of its fields has an error.
  const [delivery, setDelivery] = useState(() =>
    Object.entries(values).some(([k, v]) => k.startsWith("settings.") && v !== ""),
  );
  if (!delivery && errors.some((e) => e.field.startsWith("settings."))) setDelivery(true);
  const dirty = useRef(false);
  const navigate = useNavigate();
  const create = useCreateHook();
  const update = useUpdateHook(hook?.id ?? "");
  const { defaults, max } = me.policy;
  const patterns = me.roles["receiver"] ?? [];
  const channels = useMemo(() => channelChoices(patterns), [patterns]);
  const canPublish = (me.public ?? false) && (me.funnel ?? false);

  const build = () =>
    formRequest(
      Object.entries(fieldTypes).map(([n, t]) => [n, values[n] ?? "", t]),
      editing,
    );

  useEffect(() => {
    if (!dirty.current) return;
    const { body, errors: local } = build();
    if (local.length) {
      setErrors(local);
      setFormError("");
      return;
    }
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      dryRunHook(body, hook?.id, ctrl.signal).then(
        () => {
          setErrors([]);
          setFormError("");
        },
        (err) => {
          if (ctrl.signal.aborted) return;
          apply(err);
        },
      );
    }, dryRunDelay);
    return () => {
      clearTimeout(t);
      ctrl.abort();
    };
    // build reads values, and is a new function every render.
    // oxlint-disable-next-line react/exhaustive-deps
  }, [values]);

  function apply(err: unknown) {
    if (err instanceof ApiError && err.status === 422) {
      setErrors(err.fieldErrors);
      setFormError(formMessage(err, (f) => Object.hasOwn(fieldTypes, f)));
    } else {
      setFormError(err instanceof Error ? err.message : String(err));
    }
  }

  // A failed save other than a 422 is already toasted (makeQueryClient).
  function applySaveError(err: unknown) {
    if (err instanceof ApiError && err.status === 422) apply(err);
  }

  const set = (name: string) => (value: string) => {
    dirty.current = true;
    setValues((v) => ({ ...v, [name]: value }));
  };
  const err = (name: string) => fieldMessage(errors, name);
  const text = (name: string) => ({
    value: values[name] ?? "",
    onChange: (e: { target: { value: string } }) => set(name)(e.target.value),
  });

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const { body, errors: local } = build();
    if (local.length) return setErrors(local);
    const onSuccess = (h: Hook) => {
      toast.success(editing ? "Saved" : `Created ${h.path}`);
      navigate(`/hooks/${encodeURIComponent(h.id)}`);
    };
    if (editing) update.mutate(body, { onSuccess, onError: applySaveError });
    else create.mutate(body, { onSuccess, onError: applySaveError });
  }

  const pending = create.isPending || update.isPending;
  return (
    <form onSubmit={submit} className="max-w-2xl space-y-5" noValidate>
      {!editing && (
        <p className="text-muted-foreground">
          You own {me.hooks} of {me.limits.maxHooks} hooks.
        </p>
      )}
      {formError && (
        <p role="alert" className="text-destructive">
          {formError}
        </p>
      )}
      <Field
        label="Path"
        error={err("path")}
        hint={
          <>
            The URL is <code>~{me.slug}/</code> followed by this path: up to 8 segments of letters,
            digits, <code>.</code>, <code>_</code> and <code>-</code>.
          </>
        }
      >
        <Input
          {...text("path")}
          placeholder={editing ? undefined : "three random words"}
          autoComplete="off"
        />
      </Field>
      <Field
        label="Channel"
        error={err("channel")}
        hint={`Senders allowed this channel may call the hook. Your receiver patterns: ${patterns.join(", ") || "none"}.`}
      >
        <Input {...text("channel")} list="channels" placeholder="default" autoComplete="off" />
      </Field>
      <datalist id="channels">
        {channels.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </datalist>
      <Field label="Description" error={err("description")}>
        <Textarea {...text("description")} rows={2} maxLength={280} />
      </Field>
      <div className="space-y-1">
        <label className="flex items-center gap-2" htmlFor="ephemeral">
          <Switch
            id="ephemeral"
            checked={values["ephemeral"] === "true"}
            onCheckedChange={(on: boolean) => set("ephemeral")(String(on))}
          />
          Ephemeral: delete the hook once no tunnel has been attached for a while
        </label>
        {err("ephemeral") && <p className="text-xs text-destructive">{err("ephemeral")}</p>}
      </div>
      <div className="space-y-1">
        <label className="flex items-center gap-2" htmlFor="public">
          <Switch
            id="public"
            checked={values["public"] === "true"}
            disabled={!canPublish && values["public"] !== "true"}
            onCheckedChange={(on: boolean) => set("public")(String(on))}
          />
          Reachable from the internet, through the relay's Funnel
        </label>
        {!me.funnel ? (
          <p className="text-xs text-muted-foreground">
            This relay does not serve hooks on the internet.
          </p>
        ) : !me.public ? (
          <p className="text-xs text-muted-foreground">
            Your grants do not allow public hooks. A receiver grant needs{" "}
            <code>"public": true</code>.
          </p>
        ) : (
          values["public"] === "true" && (
            <p className="text-xs text-muted-foreground">
              Anyone with the public URL can call this hook. Verify webhook signatures in your app.
            </p>
          )
        )}
        {err("public") && <p className="text-xs text-destructive">{err("public")}</p>}
      </div>
      <Field
        label="Expires at"
        error={err("expiresAt")}
        hint={
          me.limits.maxHookTTL
            ? `RFC 3339, in UTC. Your grants require persistent hooks to expire within ${me.limits.maxHookTTL}.`
            : "RFC 3339, in UTC. Optional."
        }
      >
        <Input {...text("expiresAt")} placeholder="2026-12-31T00:00:00Z" autoComplete="off" />
      </Field>

      <Collapsible open={delivery} onOpenChange={setDelivery} className="border-t pt-4">
        <CollapsibleTrigger className="group flex w-full items-baseline gap-2 text-left">
          <ChevronRight className="size-4 self-center text-muted-foreground transition-transform duration-200 group-data-panel-open:rotate-90 motion-reduce:transition-none" />
          <span className="font-medium">Delivery</span>
          <span className="text-xs text-muted-foreground">
            Timeouts, limits and capture; the relay's defaults unless set
          </span>
        </CollapsibleTrigger>
        <CollapsiblePanel className="-mx-1 px-1">
          <fieldset className="space-y-4 pt-4 pb-1">
            <legend className="sr-only">Delivery</legend>
            <p className="text-xs text-muted-foreground">
              Leave a field empty to use the relay default.
            </p>
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Attach policy" error={err("settings.attachPolicy")}>
                <NativeSelect {...text("settings.attachPolicy")}>
                  <option value="">relay default ({defaults.attachPolicy})</option>
                  <option value="takeover">takeover: a new tunnel replaces the attached one</option>
                  <option value="reject">
                    reject: a new tunnel is refused while one is attached
                  </option>
                </NativeSelect>
              </Field>
              <Field label="Host header" error={err("settings.hostHeader")}>
                <NativeSelect {...text("settings.hostHeader")}>
                  <option value="">relay default ({defaults.hostHeader})</option>
                  <option value="preserve">preserve: your app sees the relay's host</option>
                  <option value="rewrite">rewrite: your app sees its own host</option>
                </NativeSelect>
              </Field>
              <Field
                label="Hold timeout"
                error={err("settings.holdTimeout")}
                hint={`How long calls wait for a tunnel; 0s disables holding.${max.holdTimeout ? ` At most ${max.holdTimeout}.` : ""}`}
              >
                <Input {...text("settings.holdTimeout")} placeholder={defaults.holdTimeout} />
              </Field>
              <Field
                label="Response timeout"
                error={err("settings.responseTimeout")}
                hint={`Until your app's response headers arrive.${max.responseTimeout ? ` At most ${max.responseTimeout}.` : ""}`}
              >
                <Input
                  {...text("settings.responseTimeout")}
                  placeholder={defaults.responseTimeout}
                />
              </Field>
              <Field
                label="Idle timeout"
                error={err("settings.idleTimeout")}
                hint={`Longest pause in body traffic.${max.idleTimeout ? ` At most ${max.idleTimeout}.` : ""}`}
              >
                <Input {...text("settings.idleTimeout")} placeholder={defaults.idleTimeout} />
              </Field>
              <Field
                label="Max body size"
                error={err("settings.maxBodyBytes")}
                hint={`In bytes; 0 means unlimited.${max.maxBodyBytes ? ` At most ${formatSize(max.maxBodyBytes)}.` : ""}`}
              >
                <Input
                  {...text("settings.maxBodyBytes")}
                  inputMode="numeric"
                  placeholder={defaults.maxBodyBytes ? String(defaults.maxBodyBytes) : "unlimited"}
                />
              </Field>
              <Field
                label="Rate limit"
                error={err("settings.rateLimit")}
                hint={`Calls per second / burst.${max.rateLimit?.perSecond ? ` At most ${max.rateLimit.perSecond}/s.` : ""}${max.rateLimit?.burst ? ` Burst at most ${max.rateLimit.burst}.` : ""}`}
              >
                <Input
                  {...text("settings.rateLimit")}
                  placeholder={`${defaults.rateLimit.perSecond}/${defaults.rateLimit.burst}`}
                />
              </Field>
              <Field
                label="Capture"
                error={err("settings.capture")}
                hint="Bodies are kept for the inspector only if capture is on. Metadata is always recorded."
              >
                <NativeSelect {...text("settings.capture")}>
                  <option value="">
                    relay default ({defaults.capture ? "bodies and metadata" : "metadata only"})
                  </option>
                  <option value="true">bodies and metadata</option>
                  <option value="false">metadata only</option>
                </NativeSelect>
              </Field>
            </div>
          </fieldset>
        </CollapsiblePanel>
      </Collapsible>

      <div className="flex gap-2">
        <Button type="submit" disabled={pending}>
          {editing ? "Save changes" : "Create hook"}
        </Button>
        <Link
          to={editing ? `/hooks/${encodeURIComponent(hook.id)}` : "/"}
          className={buttonVariants({ variant: "ghost" })}
        >
          Cancel
        </Link>
      </div>
    </form>
  );
}
