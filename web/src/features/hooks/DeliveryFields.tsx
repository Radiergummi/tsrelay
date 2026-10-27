import { useState, type ReactNode } from "react";
import type { Me } from "@/api/schemas";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import {
  RadioCard,
  RadioChip,
  RadioGroup,
  Segment,
  SegmentedControl,
} from "@/components/ui/radio-group";
import { humanDuration, parseDuration } from "@/lib/duration";
import { formatSize } from "@/lib/format";
import { Field } from "./Field";
import type { FieldName, FormValues } from "./formRequest";

/** Form is how a delivery field reads and writes the form's texts; "" is the relay default. */
export interface Form {
  values: FormValues;
  set: (name: FieldName) => (value: string) => void;
  err: (name: FieldName) => string | undefined;
}

/** durationPresets are the one-click choices per duration (issue #25). */
const durationPresets = {
  holdTimeout: ["0s", "10s", "30s", "2m", "5m"],
  responseTimeout: ["30s", "2m", "10m", "30m"],
  idleTimeout: ["10s", "1m", "5m", "30m"],
};

/** DeliveryFields are the per-hook delivery settings, each showing the relay default until changed. */
export function DeliveryFields({
  form,
  policy,
  relayHost,
  appHost,
}: {
  form: Form;
  policy: Me["policy"];
  relayHost: string;
  appHost: string | undefined;
}) {
  const { defaults, max } = policy;
  const rate = (r: { perSecond: number; burst: number }) => `${r.perSecond}/s, burst ${r.burst}`;
  return (
    <div className="space-y-5">
      <DurationSetting
        form={form}
        name="holdTimeout"
        label="Hold timeout"
        dflt={defaults.holdTimeout}
        max={max.holdTimeout}
        hint="How long calls wait for a tunnel; off fails them at once."
      />
      <DurationSetting
        form={form}
        name="responseTimeout"
        label="Response timeout"
        dflt={defaults.responseTimeout}
        max={max.responseTimeout}
        hint="Until your app's response headers arrive."
      />
      <DurationSetting
        form={form}
        name="idleTimeout"
        label="Idle timeout"
        dflt={defaults.idleTimeout}
        max={max.idleTimeout}
        hint="Longest pause in body traffic."
      />
      <BodySizeSetting form={form} dflt={defaults.maxBodyBytes} max={max.maxBodyBytes} />
      <Setting
        form={form}
        name="settings.rateLimit"
        label="Rate limit"
        shown={rate(defaults.rateLimit)}
        hint={`Calls above the rate get 429 with Retry-After.${max.rateLimit ? ` At most ${rate(max.rateLimit)}.` : ""}`}
      >
        {(c) => {
          const raw = form.values["settings.rateLimit"] ?? "";
          const [perSecond = "", burst = ""] = raw ? raw.split("/") : [];
          // Clearing a half while the other is at its default resets.
          const { perSecond: dp, burst: db } = defaults.rateLimit;
          const put = (p: string, b: string) =>
            form.set("settings.rateLimit")(
              (p === "" && b === String(db)) || (b === "" && p === String(dp)) ? "" : `${p}/${b}`,
            );
          return (
            <fieldset
              aria-labelledby={c["aria-labelledby"]}
              aria-describedby={c["aria-describedby"]}
              className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2"
            >
              <span className="flex items-center gap-2 text-sm">
                {/* The rate takes the field's id, so the field's label points at it. */}
                <Input
                  id={c.id}
                  type="number"
                  min={0}
                  step="any"
                  className="w-24"
                  aria-invalid={c["aria-invalid"]}
                  value={perSecond}
                  placeholder={String(dp)}
                  onChange={(e) => put(e.target.value, burst || String(db))}
                />
                <label htmlFor={c.id}>per second</label>
              </span>
              <span className="flex items-center gap-2 text-sm">
                <Input
                  id={`${c.id}-burst`}
                  type="number"
                  min={0}
                  step={1}
                  className="w-24"
                  aria-invalid={c["aria-invalid"]}
                  value={burst}
                  placeholder={String(db)}
                  onChange={(e) => put(perSecond || String(dp), e.target.value)}
                />
                <label htmlFor={`${c.id}-burst`}>burst</label>
              </span>
            </fieldset>
          );
        }}
      </Setting>
      <ChoiceSetting
        form={form}
        name="settings.attachPolicy"
        label="Attach policy"
        dflt={defaults.attachPolicy}
        choices={[
          ["takeover", "Take over", "A new tunnel replaces the attached one."],
          ["reject", "Reject", "A new tunnel is refused while one is attached."],
        ]}
      />
      <ChoiceSetting
        form={form}
        name="settings.hostHeader"
        label="Host header"
        dflt={defaults.hostHeader}
        choices={[
          ["preserve", "Relay's host", <code key="h">{relayHost}</code>],
          [
            "rewrite",
            "Its own host",
            appHost ? <code key="h">{appHost}</code> : "Your app's, such as localhost:3000",
          ],
        ]}
      />
      <Field
        label="Capture"
        error={form.err("settings.capture")}
        hint="Bodies are kept for the inspector only if capture is on, and replay needs them. Metadata is always recorded."
        aside={form.values["settings.capture"] ? <CustomBadge /> : null}
      >
        {(c) => (
          <SegmentedControl
            aria-labelledby={c["aria-labelledby"]}
            aria-describedby={c["aria-describedby"]}
            aria-invalid={c["aria-invalid"]}
            value={form.values["settings.capture"] ?? ""}
            onValueChange={form.set("settings.capture")}
          >
            <Segment value="">Relay default ({defaults.capture ? "on" : "off"})</Segment>
            <Segment value="true">On</Segment>
            <Segment value="false">Off</Segment>
          </SegmentedControl>
        )}
      </Field>
    </div>
  );
}

function CustomBadge() {
  return (
    <span className="rounded-full bg-accent px-1.5 text-xs leading-5 text-accent-foreground">
      custom
    </span>
  );
}

/** Setting is a delivery field: a custom badge and a reset link once it overrides the default (shown). */
function Setting({
  form,
  name,
  label,
  shown,
  hint,
  onReset,
  children,
}: {
  form: Form;
  name: FieldName;
  label: string;
  shown: string;
  hint?: ReactNode;
  onReset?: () => void;
  children: Parameters<typeof Field>[0]["children"];
}) {
  const custom = (form.values[name] ?? "") !== "";
  return (
    <Field
      label={label}
      error={form.err(name)}
      hint={hint}
      aside={
        custom && (
          <>
            <CustomBadge />
            <button
              type="button"
              aria-label={`Reset ${label.toLowerCase()} to ${shown}`}
              className="text-xs text-muted-foreground underline underline-offset-2 hover:text-foreground"
              onClick={() => {
                form.set(name)("");
                onReset?.();
              }}
            >
              reset to {shown}
            </button>
          </>
        )
      }
    >
      {children}
    </Field>
  );
}

/**
 * DurationSetting offers presets up to the operator's maximum, the default
 * among them, plus "custom…" for any Go duration. Picking the default
 * preset is the same as resetting.
 */
function DurationSetting({
  form,
  name,
  label,
  dflt,
  max,
  hint,
}: {
  form: Form;
  name: keyof typeof durationPresets;
  label: string;
  dflt: string;
  max: string | undefined;
  hint: string;
}) {
  const field = `settings.${name}` as const;
  const raw = form.values[field] ?? "";
  const maxMs = max ? parseDuration(max) : null;
  const dfltMs = parseDuration(dflt) ?? 0;
  const presets = durationPresets[name]
    .map((go) => ({ go, ms: parseDuration(go)! }))
    .filter((p) => (maxMs === null || p.ms <= maxMs) && p.ms !== dfltMs);
  presets.push({ go: dflt, ms: dfltMs });
  presets.sort((a, b) => a.ms - b.ms);
  const rawMs = raw ? parseDuration(raw) : dfltMs;
  const [custom, setCustom] = useState(() => !presets.some((p) => p.ms === rawMs));
  const say = (ms: number) => (ms === 0 ? "off" : humanDuration(ms));
  const echo = raw && rawMs !== null ? `= ${say(rawMs)}` : "";
  return (
    <Setting
      form={form}
      name={field}
      label={label}
      shown={say(dfltMs)}
      onReset={() => setCustom(false)}
      hint={`${hint}${max ? ` At most ${humanDuration(maxMs ?? 0)}; longer presets are hidden.` : ""}`}
    >
      {(c) => (
        <div className="space-y-2">
          <RadioGroup
            aria-labelledby={c["aria-labelledby"]}
            aria-describedby={c["aria-describedby"]}
            aria-invalid={c["aria-invalid"]}
            value={custom ? "custom" : String(rawMs)}
            onValueChange={(v) => {
              if (v === "custom") return setCustom(true);
              setCustom(false);
              const chosen = presets.find((p) => String(p.ms) === v)!;
              form.set(field)(chosen.ms === dfltMs ? "" : chosen.go);
            }}
          >
            {presets.map((p) => (
              <RadioChip key={p.ms} value={String(p.ms)} inherited={raw === ""}>
                {say(p.ms)}
                {p.ms === dfltMs && <span className="text-xs opacity-70">default</span>}
              </RadioChip>
            ))}
            <RadioChip value="custom">custom…</RadioChip>
          </RadioGroup>
          {custom && (
            <div className="flex items-center gap-2">
              <Input
                className="w-32 font-mono"
                aria-label={`${label}, custom`}
                aria-describedby={c["aria-describedby"]}
                aria-invalid={c["aria-invalid"]}
                placeholder={dflt}
                value={raw}
                onChange={(e) => form.set(field)(e.target.value)}
                autoComplete="off"
              />
              <span className="text-xs text-muted-foreground" aria-live="polite">
                {echo || "Go duration syntax: 90s, 1m30s, 2h"}
              </span>
            </div>
          )}
        </div>
      )}
    </Setting>
  );
}

const units: [string, number][] = [
  ["KiB", 1024],
  ["MiB", 1 << 20],
  ["GiB", 1 << 30],
];

/** BodySizeSetting is a number and a unit, stored as bytes, with "unlimited" (0) unless the relay has a maximum. */
function BodySizeSetting({
  form,
  dflt,
  max,
}: {
  form: Form;
  dflt: number;
  max: number | undefined;
}) {
  const field = "settings.maxBodyBytes";
  const raw = form.values[field] ?? "";
  const [unit, setUnit] = useState(() => {
    const n = raw ? Number(raw) : dflt;
    return [...units].reverse().find(([, size]) => n >= size && n % size === 0)?.[1] ?? 1 << 20;
  });
  // The typed text, kept while it still says what raw does, so "1." survives.
  const [text, setText] = useState("");
  const derived = raw === "" || raw === "0" ? "" : String(Number((Number(raw) / unit).toFixed(3)));
  const shown = text !== "" && toBytes(text, unit) === raw ? text : derived;
  const put = (t: string, size: number) => {
    setText(t);
    form.set(field)(t === "" ? "" : toBytes(t, size));
  };
  const show = (n: number) => (n > 0 ? formatSize(n) : "unlimited");
  return (
    <Setting
      form={form}
      name={field}
      label="Max body size"
      onReset={() => setText("")}
      shown={show(dflt)}
      hint={max ? `At most ${formatSize(max)} on this relay.` : "This relay has no maximum."}
    >
      {(c) => (
        <fieldset
          aria-labelledby={c["aria-labelledby"]}
          aria-describedby={c["aria-describedby"]}
          className="flex min-w-0 flex-wrap items-center gap-2"
        >
          <Input
            type="number"
            min={0}
            step="any"
            className="w-28"
            aria-label="Size"
            aria-invalid={c["aria-invalid"]}
            value={shown}
            placeholder={dflt > 0 ? String(Number((dflt / unit).toFixed(3))) : "unlimited"}
            onChange={(e) => put(e.target.value, unit)}
          />
          <NativeSelect
            aria-label="Unit"
            className="w-20"
            value={unit}
            onChange={(e) => {
              const size = Number(e.target.value);
              setUnit(size);
              if (shown !== "") put(shown, size);
            }}
          >
            {units.map(([name, size]) => (
              <option key={name} value={size}>
                {name}
              </option>
            ))}
          </NativeSelect>
          {!max && (
            <button
              type="button"
              aria-pressed={raw === "0"}
              className="inline-flex h-7 items-center rounded-full border border-input px-2.5 text-sm outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 aria-pressed:border-primary aria-pressed:bg-primary aria-pressed:text-primary-foreground"
              onClick={() => {
                setText("");
                form.set(field)(raw === "0" ? "" : "0");
              }}
            >
              unlimited
            </button>
          )}
        </fieldset>
      )}
    </Setting>
  );
}

const toBytes = (t: string, size: number) => String(Math.round(Number(t) * size));

/** ChoiceSetting is a set of radio cards, the default one marked; picking it resets. */
function ChoiceSetting({
  form,
  name,
  label,
  dflt,
  choices,
}: {
  form: Form;
  name: FieldName;
  label: string;
  dflt: string;
  choices: [value: string, title: string, about: ReactNode][];
}) {
  const raw = form.values[name] ?? "";
  const title = (v: string) => choices.find(([c]) => c === v)?.[1] ?? v;
  return (
    <Setting form={form} name={name} label={label} shown={title(dflt)}>
      {(c) => (
        <RadioGroup
          aria-labelledby={c["aria-labelledby"]}
          aria-describedby={c["aria-describedby"]}
          aria-invalid={c["aria-invalid"]}
          className="grid gap-2 sm:grid-cols-2"
          value={raw || dflt}
          onValueChange={(v) => form.set(name)(v === dflt ? "" : v)}
        >
          {choices.map(([value, t, about]) => (
            <RadioCard key={value} value={value}>
              <span className="flex items-center gap-2 font-medium">
                {t}
                {value === dflt && (
                  <span className="text-xs font-normal text-muted-foreground">default</span>
                )}
              </span>
              <span className="text-xs text-muted-foreground">{about}</span>
            </RadioCard>
          ))}
        </RadioGroup>
      )}
    </Setting>
  );
}
