/** Characters that change how surrounding text displays, or don't display at all. */
const controls =
  // eslint-disable-next-line no-control-regex -- matching these control characters is the point.
  /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f\u0080-\u009f\u061c\u200b-\u200f\u2028\u2029\u202a-\u202e\u2060-\u2064\u2066-\u2069\ufeff]+/g;

/** splitControls separates the runs of characters Reveal shows as ⟨U+XXXX⟩ from the rest. A run, not one character at a time, keeps a body of mostly control characters from becoming one span per character. */
export function splitControls(text: string): { control: boolean; text: string }[] {
  const out: { control: boolean; text: string }[] = [];
  let last = 0;
  for (const match of text.matchAll(controls)) {
    const [run] = match;
    if (match.index > last) out.push({ control: false, text: text.slice(last, match.index) });
    out.push({ control: true, text: run });
    last = match.index + run.length;
  }
  if (last < text.length) out.push({ control: false, text: text.slice(last) });
  return out;
}

const code = (character: string) =>
  `⟨U+${character.codePointAt(0)!.toString(16).toUpperCase().padStart(4, "0")}⟩`;

/** codes renders every character of a control run as its own ⟨U+XXXX⟩, joined. */
const codes = (run: string) => [...run].map(code).join("");

/**
 * Reveal renders captured text as text, with invisible and bidi control
 * characters shown as ⟨U+XXXX⟩, so "abc\u202edef" can't pretend to be
 * something else. Copy buttons copy the original.
 */
export function Reveal({ text }: { text: string }) {
  const parts = splitControls(text);
  if (parts.length === 1 && !parts[0]!.control) return <>{text}</>;
  return (
    <>
      {parts.map((part, i) =>
        part.control ? (
          <span
            key={i}
            className="rounded-sm bg-muted px-0.5 text-[0.85em] text-destructive"
            title="control character"
          >
            {codes(part.text)}
          </span>
        ) : (
          <span key={i}>{part.text}</span>
        ),
      )}
    </>
  );
}
