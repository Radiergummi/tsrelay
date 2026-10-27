import { useState, type ReactNode } from "react";
import type { Me } from "@/api/schemas";
import { Input } from "@/components/ui/input";
import { NativeSelect } from "@/components/ui/native-select";
import {
  chip,
  RadioCard,
  RadioChip,
  RadioGroup,
  Segment,
  SegmentedControl,
} from "@/components/ui/radio-group";
import { humanDuration, parseDuration } from "@/lib/duration";
import { formatSize } from "@/lib/format";
import { cn } from "@/lib/utils";
import { Field } from "./Field";
import type { FieldName, FormValues } from "./formRequest";

/** Form is how a delivery field reads and writes the form's texts; "" is the relay default. */
export interface Form {
  values: FormValues;
  set: (name: FieldName) => (value: string) => void;
  message: (name: FieldName) => string | undefined;
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
  const rate = ({ perSecond, burst }: { perSecond: number; burst: number }) =>
    `${perSecond}/s, burst ${burst}`;
  return (
    <div className="space-y-5">
      <DurationSetting
        form={form}
        name="holdTimeout"
        label="Hold timeout"
        relayDefault={defaults.holdTimeout}
        max={max.holdTimeout}
        hint="How long calls wait for a tunnel; off fails them at once."
      />
      <DurationSetting
        form={form}
        name="responseTimeout"
        label="Response timeout"
        relayDefault={defaults.responseTimeout}
        max={max.responseTimeout}
        hint="Until your app's response headers arrive."
      />
      <DurationSetting
        form={form}
        name="idleTimeout"
        label="Idle timeout"
        relayDefault={defaults.idleTimeout}
        max={max.idleTimeout}
        hint="Longest pause in body traffic."
      />
      <BodySizeSetting form={form} relayDefault={defaults.maxBodyBytes} max={max.maxBodyBytes} />
      <Setting
        form={form}
        name="settings.rateLimit"
        label="Rate limit"
        shown={rate(defaults.rateLimit)}
        hint={`Calls above the rate get 429 with Retry-After.${max.rateLimit ? ` At most ${rate(max.rateLimit)}.` : ""}`}
      >
        {(control) => {
          const raw = form.values["settings.rateLimit"];
          const [perSecond = "", burst = ""] = raw ? raw.split("/") : [];
          // Clearing a half while the other is at its default resets.
          const { perSecond: defaultPerSecond, burst: defaultBurst } = defaults.rateLimit;
          const put = (nextPerSecond: string, nextBurst: string) =>
            form.set("settings.rateLimit")(
              (nextPerSecond === "" && nextBurst === String(defaultBurst)) ||
                (nextBurst === "" && nextPerSecond === String(defaultPerSecond))
                ? ""
                : `${nextPerSecond}/${nextBurst}`,
            );
          return (
            <fieldset
              aria-labelledby={control["aria-labelledby"]}
              aria-describedby={control["aria-describedby"]}
              className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2"
            >
              <span className="flex items-center gap-2 text-sm">
                {/* The rate takes the field's id, so the field's label points at it. */}
                <Input
                  id={control.id}
                  type="number"
                  min={0}
                  step="any"
                  className="w-24"
                  aria-invalid={control["aria-invalid"]}
                  value={perSecond}
                  placeholder={String(defaultPerSecond)}
                  onChange={(event) => put(event.target.value, burst || String(defaultBurst))}
                />
                <label htmlFor={control.id}>per second</label>
              </span>
              <span className="flex items-center gap-2 text-sm">
                <Input
                  id={`${control.id}-burst`}
                  type="number"
                  min={0}
                  step={1}
                  className="w-24"
                  aria-invalid={control["aria-invalid"]}
                  value={burst}
                  placeholder={String(defaultBurst)}
                  onChange={(event) =>
                    put(perSecond || String(defaultPerSecond), event.target.value)
                  }
                />
                <label htmlFor={`${control.id}-burst`}>burst</label>
              </span>
            </fieldset>
          );
        }}
      </Setting>
      <ChoiceSetting
        form={form}
        name="settings.attachPolicy"
        label="Attach policy"
        relayDefault={defaults.attachPolicy}
        choices={[
          ["takeover", "Take over", "A new tunnel replaces the attached one."],
          ["reject", "Reject", "A new tunnel is refused while one is attached."],
        ]}
      />
      <ChoiceSetting
        form={form}
        name="settings.hostHeader"
        label="Host header"
        relayDefault={defaults.hostHeader}
        choices={[
          ["preserve", "Relay's host", <code key="host">{relayHost}</code>],
          [
            "rewrite",
            "Its own host",
            appHost ? <code key="host">{appHost}</code> : "Your app's, such as localhost:3000",
          ],
        ]}
      />
      <Field
        label="Capture"
        error={form.message("settings.capture")}
        hint="Bodies are kept for the inspector only if capture is on, and replay needs them. Metadata is always recorded."
        aside={form.values["settings.capture"] ? <CustomBadge /> : null}
      >
        {(control) => (
          <SegmentedControl
            aria-labelledby={control["aria-labelledby"]}
            aria-describedby={control["aria-describedby"]}
            aria-invalid={control["aria-invalid"]}
            value={form.values["settings.capture"]}
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
  const custom = form.values[name] !== "";
  return (
    <Field
      label={label}
      error={form.message(name)}
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
  relayDefault,
  max,
  hint,
}: {
  form: Form;
  name: keyof typeof durationPresets;
  label: string;
  relayDefault: string;
  max: string | undefined;
  hint: string;
}) {
  const field = `settings.${name}` as const;
  const raw = form.values[field];
  const maxMilliseconds = max ? parseDuration(max) : null;
  const defaultMilliseconds = parseDuration(relayDefault) ?? 0;
  const presets = durationPresets[name]
    .map((go) => ({ go, milliseconds: parseDuration(go)! }))
    .filter(
      ({ milliseconds }) =>
        (maxMilliseconds === null || milliseconds <= maxMilliseconds) &&
        milliseconds !== defaultMilliseconds,
    );
  presets.push({ go: relayDefault, milliseconds: defaultMilliseconds });
  presets.sort((a, b) => a.milliseconds - b.milliseconds);
  const rawMilliseconds = raw ? parseDuration(raw) : defaultMilliseconds;
  const [custom, setCustom] = useState(
    () => !presets.some(({ milliseconds }) => milliseconds === rawMilliseconds),
  );
  const say = (milliseconds: number) => (milliseconds === 0 ? "off" : humanDuration(milliseconds));
  const echo = raw && rawMilliseconds !== null ? `= ${say(rawMilliseconds)}` : "";
  return (
    <Setting
      form={form}
      name={field}
      label={label}
      shown={say(defaultMilliseconds)}
      onReset={() => setCustom(false)}
      hint={`${hint}${max ? ` At most ${humanDuration(maxMilliseconds ?? 0)}; longer presets are hidden.` : ""}`}
    >
      {(control) => (
        <div className="space-y-2">
          <RadioGroup
            aria-labelledby={control["aria-labelledby"]}
            aria-describedby={control["aria-describedby"]}
            aria-invalid={control["aria-invalid"]}
            value={custom ? "custom" : String(rawMilliseconds)}
            onValueChange={(value) => {
              if (value === "custom") return setCustom(true);
              setCustom(false);
              const chosen = presets.find(({ milliseconds }) => String(milliseconds) === value)!;
              form.set(field)(chosen.milliseconds === defaultMilliseconds ? "" : chosen.go);
            }}
          >
            {presets.map(({ milliseconds }) => (
              <RadioChip key={milliseconds} value={String(milliseconds)} inherited={raw === ""}>
                {say(milliseconds)}
                {milliseconds === defaultMilliseconds && (
                  <span className="text-xs opacity-70">default</span>
                )}
              </RadioChip>
            ))}
            <RadioChip value="custom">custom…</RadioChip>
          </RadioGroup>
          {custom && (
            <div className="flex items-center gap-2">
              <Input
                className="w-32 font-mono"
                aria-label={`${label}, custom`}
                aria-describedby={control["aria-describedby"]}
                aria-invalid={control["aria-invalid"]}
                placeholder={relayDefault}
                value={raw}
                onChange={(event) => form.set(field)(event.target.value)}
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
  relayDefault,
  max,
}: {
  form: Form;
  relayDefault: number;
  max: number | undefined;
}) {
  const field = "settings.maxBodyBytes";
  const raw = form.values[field];
  const [unit, setUnit] = useState(() => {
    const bytes = raw ? Number(raw) : relayDefault;
    return (
      [...units].reverse().find(([, size]) => bytes >= size && bytes % size === 0)?.[1] ?? 1 << 20
    );
  });
  // The typed text, kept while it still says what raw does, so "1." survives.
  const [text, setText] = useState("");
  const derived = raw === "" || raw === "0" ? "" : String(Number((Number(raw) / unit).toFixed(3)));
  const shown = text !== "" && toBytes(text, unit) === raw ? text : derived;
  const put = (typed: string, size: number) => {
    setText(typed);
    form.set(field)(typed === "" ? "" : toBytes(typed, size));
  };
  const show = (bytes: number) => (bytes > 0 ? formatSize(bytes) : "unlimited");
  return (
    <Setting
      form={form}
      name={field}
      label="Max body size"
      onReset={() => setText("")}
      shown={show(relayDefault)}
      hint={max ? `At most ${formatSize(max)} on this relay.` : "This relay has no maximum."}
    >
      {(control) => (
        <fieldset
          aria-labelledby={control["aria-labelledby"]}
          aria-describedby={control["aria-describedby"]}
          className="flex min-w-0 flex-wrap items-center gap-2"
        >
          <Input
            type="number"
            min={0}
            step="any"
            className="w-28"
            aria-label="Size"
            aria-invalid={control["aria-invalid"]}
            value={shown}
            placeholder={
              relayDefault > 0 ? String(Number((relayDefault / unit).toFixed(3))) : "unlimited"
            }
            onChange={(event) => put(event.target.value, unit)}
          />
          <NativeSelect
            aria-label="Unit"
            className="w-20"
            value={unit}
            onChange={(event) => {
              const size = Number(event.target.value);
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
              className={cn(
                chip,
                "aria-pressed:border-primary aria-pressed:bg-primary aria-pressed:text-primary-foreground",
              )}
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

const toBytes = (typed: string, size: number) => String(Math.round(Number(typed) * size));

/** ChoiceSetting is a set of radio cards, the default one marked; picking it resets. */
function ChoiceSetting({
  form,
  name,
  label,
  relayDefault,
  choices,
}: {
  form: Form;
  name: FieldName;
  label: string;
  relayDefault: string;
  choices: [value: string, title: string, about: ReactNode][];
}) {
  const raw = form.values[name];
  const titleOf = (value: string) => choices.find(([choice]) => choice === value)?.[1] ?? value;
  return (
    <Setting form={form} name={name} label={label} shown={titleOf(relayDefault)}>
      {(control) => (
        <RadioGroup
          aria-labelledby={control["aria-labelledby"]}
          aria-describedby={control["aria-describedby"]}
          aria-invalid={control["aria-invalid"]}
          className="grid gap-2 sm:grid-cols-2"
          value={raw || relayDefault}
          onValueChange={(value) => form.set(name)(value === relayDefault ? "" : value)}
        >
          {choices.map(([value, title, about]) => (
            <RadioCard key={value} value={value}>
              <span className="flex items-center gap-2 font-medium">
                {title}
                {value === relayDefault && (
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
