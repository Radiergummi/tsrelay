import { ChevronRight } from "lucide-react";
import { useEffect, useEffectEvent, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router";
import { paths } from "@/api/client";
import { dryRunHook, useCreateHook, useUpdateHook } from "@/api/mutations";
import { useChannels } from "@/api/queries";
import type { Effective, Hook, Me } from "@/api/schemas";
import { Button, buttonVariants } from "@/components/ui/button";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Combobox } from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Segment, SegmentedControl } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
import { allowingPattern, channelChoices, matchChannel, validChannel } from "@/lib/channels";
import { toast } from "@/lib/toast";
import { useProblemErrors } from "@/lib/useProblemErrors";
import { DeliveryFields } from "./DeliveryFields";
import { ExpiryField } from "./ExpiryField";
import { Field } from "./Field";
import {
  effectiveSettings,
  fieldTypes,
  formRequest,
  initialValues,
  type FieldName,
} from "./formRequest";
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
  const { errors, formError, show, fail, capture, message } = useProblemErrors((field) =>
    Object.hasOwn(fieldTypes, field),
  );
  // The last dry run that passed, keyed by the body it validated.
  const [dryRun, setDryRun] = useState<{ key: string; hook: Hook }>();
  // Delivery starts shut unless the hook overrides a default, and opens
  // (and stays open) when one of its fields has an error.
  const overrides = Object.entries(values).filter(
    ([name, value]) => name.startsWith("settings.") && value !== "",
  ).length;
  const [delivery, setDelivery] = useState(overrides > 0);
  if (!delivery && errors.some(({ field }) => field.startsWith("settings."))) setDelivery(true);
  // Nothing is validated until the first change.
  const [dirty, setDirty] = useState(false);
  const navigate = useNavigate();
  const create = useCreateHook();
  const update = useUpdateHook(hook?.id ?? "");
  const inUse = useChannels(false).data?.channels;
  const { defaults } = me.policy;
  const patterns = useMemo(() => me.roles["receiver"] ?? [], [me]);
  const channels = useMemo(() => {
    const choices = channelChoices(patterns);
    for (const { name } of inUse ?? [])
      if (!choices.includes(name) && allowingPattern(patterns, name)) choices.push(name);
    return choices;
  }, [patterns, inUse]);
  const channel = values.channel.trim() || "default";
  const pattern = allowingPattern(patterns, channel);
  const canPublishHere = me.publicPatterns.some((publicPattern) =>
    matchChannel(publicPattern, channel),
  );
  const canPublish = canPublishHere && (me.funnel ?? false);

  // The hook's URL is base followed by its path. A new hook has no URL
  // yet, and the entry document has no base URL, so it is this origin's.
  const base = hook
    ? hook.url.slice(0, hook.url.length - hook.path.length)
    : `${window.location.origin}/~${me.slug ?? ""}/`;
  const tilde = base.indexOf("/~") + 1;

  const build = () => {
    const request = formRequest(values, editing ? initial : undefined);
    if (!validChannel(channel))
      request.errors.push({
        field: "channel",
        message: "must be dot-separated segments of a-z, 0-9 and -, at most 64 characters",
      });
    else if (!pattern)
      request.errors.push({ field: "channel", message: "none of your receive patterns allows it" });
    return request;
  };
  const { body, errors: local } = build();
  // Two different invalid fields can leave the body the same, so the key
  // holds the local errors too.
  const key = JSON.stringify([body, local]);
  const invalid = local.length > 0;

  // Shows the local errors, or else dry-runs the body, whose key is ran: a
  // change since would have cancelled this run.
  const validate = useEffectEvent((ran: string, signal: AbortSignal) => {
    if (invalid) return show(local);
    dryRunHook(body, hook?.id, signal).then(
      (validated) => {
        setDryRun({ key: ran, hook: validated });
        show([]);
      },
      (error) => {
        if (signal.aborted || capture(error)) return;
        fail(error instanceof Error ? error.message : String(error));
      },
    );
  });

  useEffect(() => {
    if (!dirty) return;
    const controller = new AbortController();
    const timer = setTimeout(() => validate(key, controller.signal), invalid ? 0 : dryRunDelay);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [dirty, key, invalid]);

  const set = (name: FieldName) => (value: string) => {
    setDirty(true);
    setValues((previous) => ({ ...previous, [name]: value }));
  };
  const text = (name: FieldName) => ({
    value: values[name],
    onChange: (event: { target: { value: string } }) => set(name)(event.target.value),
  });

  function submit(event: React.FormEvent) {
    event.preventDefault();
    if (local.length) return show(local);
    const onSuccess = (saved: Hook) => {
      toast.success(editing ? "Saved" : `Created ${saved.path}`);
      navigate(paths.hook(saved.id));
    };
    // A failed save other than a 422 is already toasted (makeQueryClient).
    if (editing) update.mutate(body, { onSuccess, onError: capture });
    else create.mutate(body, { onSuccess, onError: capture });
  }

  // The preview shows the dry run's hook while it matches the form, and
  // what the form would give otherwise: before the first dry run, and
  // while the next one is pending or failed.
  const current = dryRun?.key === key ? dryRun.hook : undefined;
  const effective: Effective = current?.effective ?? effectiveSettings(values, defaults);
  const path = values.path.trim();
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
            error={message("path")}
            hint={
              <>
                Up to 8 segments of letters, digits, <code>.</code>, <code>_</code> and{" "}
                <code>-</code>.{editing ? "" : " Leave it empty for three random words."}
              </>
            }
          >
            {(control) => (
              <div className="flex min-w-0">
                <span className="flex items-center rounded-l-lg border border-r-0 border-input bg-muted px-2 font-mono text-xs whitespace-nowrap text-muted-foreground">
                  <span className="hidden sm:inline">{base.slice(0, tilde)}</span>
                  {base.slice(tilde)}
                </span>
                <Input
                  {...text("path")}
                  id={control.id}
                  aria-invalid={control["aria-invalid"]}
                  aria-describedby={control["aria-describedby"]}
                  className="rounded-l-none font-mono"
                  placeholder={editing ? undefined : "three random words"}
                  autoComplete="off"
                />
              </div>
            )}
          </Field>
          <Field
            label="Channel"
            error={message("channel")}
            hint={
              <>
                {pattern && !message("channel") && (
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
              value={values.channel}
              onValueChange={set("channel")}
              placeholder="default"
              autoComplete="off"
            />
          </Field>
          <Field label="Description" error={message("description")}>
            <Textarea {...text("description")} rows={2} maxLength={280} />
          </Field>
          <Field
            label="Reachable from"
            error={message("public")}
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
            {(control) => (
              <SegmentedControl
                aria-labelledby={control["aria-labelledby"]}
                aria-describedby={control["aria-describedby"]}
                aria-invalid={control["aria-invalid"]}
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
            error={message("ephemeral")}
            hint="Ephemeral hooks are deleted once no tunnel has been attached for a while."
          >
            {(control) => (
              <SegmentedControl
                aria-labelledby={control["aria-labelledby"]}
                aria-describedby={control["aria-describedby"]}
                aria-invalid={control["aria-invalid"]}
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
            error={message("expiresAt")}
            hint={`In your time zone, ${new Intl.DateTimeFormat().resolvedOptions().timeZone}.`}
          >
            {(control) => (
              <ExpiryField
                control={control}
                value={values.expiresAt}
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
                form={{ values, set, message }}
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
        expiresAt={values.expiresAt}
        effective={effective}
        inherited={(setting) => values[`settings.${setting}`] === ""}
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
