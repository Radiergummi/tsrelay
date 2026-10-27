import { ChevronRight } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate } from "react-router";
import { ApiError, fieldMessage, formMessage, paths } from "@/api/client";
import { dryRunHook, useCreateHook, useUpdateHook } from "@/api/mutations";
import { useChannels } from "@/api/queries";
import type { Effective, FieldError, Hook, Me } from "@/api/schemas";
import { Button, buttonVariants } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Combobox } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Segment, SegmentedControl } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
import { allowingPattern, channelChoices, matchChannel, validChannel } from "@/lib/channels";
import { toast } from "@/lib/toast";
import { DeliveryFields } from "./DeliveryFields";
import { ExpiryField } from "./ExpiryField";
import { Field } from "./Field";
import { effectiveSettings, fieldTypes, formRequest, initialValues } from "./formRequest";
import { HookPreview } from "./HookPreview";

const dryRunDelay = 400;

/**
 * HookForm creates a hook, or edits hook. Every change is validated by a
 * dry run, whose result the preview beside the form shows.
 */
export function HookForm({ me, hook }: { me: Me; hook?: Hook }) {
  const editing = hook !== undefined;
  const [initial] = useState(() => initialValues(hook));
  const [values, setValues] = useState(initial);
  const [errors, setErrors] = useState<FieldError[]>([]);
  const [formError, setFormError] = useState("");
  // The last dry run that passed, keyed by the body it validated.
  const [dry, setDry] = useState<{ key: string; hook: Hook }>();
  // Delivery starts shut unless the hook overrides a default, and opens
  // (and stays open) when one of its fields has an error.
  const overrides = Object.entries(values).filter(
    ([k, v]) => k.startsWith("settings.") && v !== "",
  ).length;
  const [delivery, setDelivery] = useState(overrides > 0);
  if (!delivery && errors.some((e) => e.field.startsWith("settings."))) setDelivery(true);
  const dirty = useRef(false);
  const navigate = useNavigate();
  const create = useCreateHook();
  const update = useUpdateHook(hook?.id ?? "");
  const inUse = useChannels(false).data?.channels;
  const { defaults } = me.policy;
  const patterns = useMemo(() => me.roles["receiver"] ?? [], [me]);
  const channels = useMemo(() => {
    const out = channelChoices(patterns);
    for (const c of inUse ?? [])
      if (!out.includes(c.name) && allowingPattern(patterns, c.name)) out.push(c.name);
    return out;
  }, [patterns, inUse]);
  const channel = values["channel"]?.trim() || "default";
  const pattern = allowingPattern(patterns, channel);
  const canPublishHere = me.publicPatterns.some((p) => matchChannel(p, channel));
  const canPublish = canPublishHere && (me.funnel ?? false);

  // The hook's URL is base followed by its path. A new hook has no URL
  // yet, and the entry document has no base URL, so it is this origin's.
  const base = hook
    ? hook.url.slice(0, hook.url.length - hook.path.length)
    : `${window.location.origin}/~${me.slug ?? ""}/`;
  const tilde = base.indexOf("/~") + 1;

  const build = () => {
    const r = formRequest(
      Object.entries(fieldTypes).map(([n, t]) => [n, values[n] ?? "", t]),
      editing ? initial : undefined,
    );
    if (!validChannel(channel))
      r.errors.push({
        field: "channel",
        message: "must be dot-separated segments of a-z, 0-9 and -, at most 64 characters",
      });
    else if (!pattern)
      r.errors.push({ field: "channel", message: "none of your receive patterns allows it" });
    return r;
  };
  const { body } = build();
  const key = JSON.stringify(body);

  useEffect(() => {
    if (!dirty.current) return;
    const { errors: local } = build();
    if (local.length) {
      setErrors(local);
      setFormError("");
      return;
    }
    const ctrl = new AbortController();
    const t = setTimeout(() => {
      dryRunHook(body, hook?.id, ctrl.signal).then(
        (h) => {
          setDry({ key, hook: h });
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
    // build reads values, and body and key follow from them.
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
    const { errors: local } = build();
    if (local.length) return setErrors(local);
    const onSuccess = (h: Hook) => {
      toast.success(editing ? "Saved" : `Created ${h.path}`);
      navigate(paths.hook(h.id));
    };
    if (editing) update.mutate(body, { onSuccess, onError: applySaveError });
    else create.mutate(body, { onSuccess, onError: applySaveError });
  }

  // The preview shows the dry run's hook while it matches the form, and
  // what the form would give otherwise: before the first dry run, and
  // while the next one is pending or failed.
  const current = dry?.key === key ? dry.hook : undefined;
  const effective: Effective = current?.effective ?? effectiveSettings(values, defaults);
  const path = values["path"]?.trim() ?? "";
  const ephemeral = values["ephemeral"] === "true";
  const pending = create.isPending || update.isPending;
  const group = "space-y-4";
  const legend = "mb-3 text-xs font-medium tracking-wide text-muted-foreground uppercase";
  return (
    <form
      onSubmit={submit}
      className="grid max-w-5xl gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]"
      noValidate
    >
      <div className="min-w-0 space-y-6">
        {!editing && (
          <p className="text-muted-foreground">
            You own {me.hookCount} of {me.limits.maxHooks} hooks.
          </p>
        )}
        {formError && (
          <p role="alert" className="text-destructive">
            {formError}
          </p>
        )}
        <fieldset className={group}>
          <legend className={legend}>Address</legend>
          <Field
            label="Path"
            error={err("path")}
            hint={
              <>
                Up to 8 segments of letters, digits, <code>.</code>, <code>_</code> and{" "}
                <code>-</code>.{editing ? "" : " Leave it empty for three random words."}
              </>
            }
          >
            {(c) => (
              <div className="flex min-w-0">
                <span className="flex items-center rounded-l-lg border border-r-0 border-input bg-muted px-2 font-mono text-xs whitespace-nowrap text-muted-foreground">
                  <span className="hidden sm:inline">{base.slice(0, tilde)}</span>
                  {base.slice(tilde)}
                </span>
                <Input
                  {...text("path")}
                  id={c.id}
                  aria-invalid={c["aria-invalid"]}
                  aria-describedby={c["aria-describedby"]}
                  className="rounded-l-none font-mono"
                  placeholder={editing ? undefined : "three random words"}
                  autoComplete="off"
                />
              </div>
            )}
          </Field>
          <Field
            label="Channel"
            error={err("channel")}
            hint={
              <>
                {pattern && !err("channel") && (
                  <>
                    ✓ allowed by <code>{pattern}</code>.{" "}
                  </>
                )}
                Senders allowed on a channel may call its hooks. Names are dot-separated segments of
                a-z, 0-9 and -.
              </>
            }
          >
            <Combobox
              items={channels}
              value={values["channel"] ?? ""}
              onValueChange={set("channel")}
              placeholder="default"
              autoComplete="off"
            />
          </Field>
          <Field label="Description" error={err("description")}>
            <Textarea {...text("description")} rows={2} maxLength={280} />
          </Field>
          <Field
            label="Reachable from"
            error={err("public")}
            hint={
              !me.funnel
                ? "This relay does not serve hooks on the internet."
                : !canPublishHere
                  ? `Your grants do not allow public hooks in channel "${channel}". A receiver grant needs "public": true.`
                  : values["public"] === "true"
                    ? "Anyone with the public URL can call this hook. Verify webhook signatures in your app."
                    : "The internet reaches the hook at a secret URL, through the relay's Funnel."
            }
          >
            {(c) => (
              <SegmentedControl
                aria-labelledby={c["aria-labelledby"]}
                aria-describedby={c["aria-describedby"]}
                aria-invalid={c["aria-invalid"]}
                value={values["public"] ?? "false"}
                onValueChange={set("public")}
              >
                <Segment value="false">Tailnet only</Segment>
                <Segment value="true" disabled={!canPublish && values["public"] !== "true"}>
                  Tailnet and internet
                </Segment>
              </SegmentedControl>
            )}
          </Field>
        </fieldset>

        <fieldset className={group}>
          <legend className={legend}>Lifetime</legend>
          <Field
            label="Kind"
            error={err("ephemeral")}
            hint="Ephemeral hooks are deleted once no tunnel has been attached for a while."
          >
            {(c) => (
              <SegmentedControl
                aria-labelledby={c["aria-labelledby"]}
                aria-describedby={c["aria-describedby"]}
                aria-invalid={c["aria-invalid"]}
                value={values["ephemeral"] ?? "false"}
                onValueChange={set("ephemeral")}
              >
                <Segment value="false">Persistent</Segment>
                <Segment value="true">Ephemeral</Segment>
              </SegmentedControl>
            )}
          </Field>
          <Field
            label="Expires at"
            error={err("expiresAt")}
            hint={`In your time zone, ${Intl.DateTimeFormat().resolvedOptions().timeZone}.`}
          >
            {(c) => (
              <ExpiryField
                control={c}
                value={values["expiresAt"] ?? ""}
                onChange={set("expiresAt")}
                maxTTL={ephemeral ? undefined : me.limits.maxHookTtl}
              />
            )}
          </Field>
        </fieldset>

        <Collapsible open={delivery} onOpenChange={setDelivery} className="border-t pt-4">
          <CollapsibleTrigger className="group flex w-full items-baseline gap-2 text-left">
            <ChevronRight className="size-4 self-center text-muted-foreground transition-transform duration-200 group-data-panel-open:rotate-90 motion-reduce:transition-none" />
            <span className="font-medium">Delivery</span>
            <span className="text-xs text-muted-foreground">
              {overrides === 0
                ? "all at the relay defaults"
                : `${overrides} ${overrides === 1 ? "setting differs" : "settings differ"} from the relay defaults`}
            </span>
          </CollapsibleTrigger>
          <CollapsiblePanel className="-mx-1 px-1">
            <fieldset className="pt-4 pb-1">
              <legend className="sr-only">Delivery</legend>
              <DeliveryFields
                form={{ values, set, err }}
                policy={me.policy}
                relayHost={hostOf(base)}
                appHost={hook?.tunnel?.target && hostOf(hook.tunnel.target)}
              />
            </fieldset>
          </CollapsiblePanel>
        </Collapsible>
      </div>

      <HookPreview
        className="self-start lg:sticky lg:top-4 lg:col-start-2 lg:row-span-2 lg:row-start-1"
        url={path ? (current?.url ?? base + path) : undefined}
        channel={channel}
        pattern={pattern}
        ephemeral={ephemeral}
        expiresAt={values["expiresAt"] ?? ""}
        effective={effective}
        inherited={(k) => (values[`settings.${k}`] ?? "") === ""}
      />

      <div className="flex gap-2">
        <Button type="submit" disabled={pending}>
          {editing ? "Save changes" : "Create hook"}
        </Button>
        <Link
          to={editing ? paths.hook(hook.id) : "/hooks"}
          className={buttonVariants({ variant: "ghost" })}
        >
          Cancel
        </Link>
      </div>
    </form>
  );
}

/** hostOf is a URL's host, or the text itself if it isn't a URL with one. */
function hostOf(url: string): string {
  try {
    return new URL(url).host || url;
  } catch {
    return url;
  }
}
