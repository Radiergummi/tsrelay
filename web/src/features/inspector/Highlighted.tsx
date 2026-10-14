import type { BodyKind } from "./bytes";
import { Reveal } from "./Reveal";
import { tokenize, type TokenKind } from "./tokenize";

/** Larger texts render as plain text: highlighting a megabyte of spans would freeze the tab. */
export const highlightLimit = 256 * 1024;

const colour: Record<TokenKind, string> = {
  plain: "",
  punct: "text-muted-foreground",
  key: "text-primary",
  string: "text-status-2xx",
  number: "text-status-3xx",
  literal: "text-status-4xx",
  tag: "text-primary",
  attr: "text-status-3xx",
  value: "text-status-2xx",
  comment: "text-muted-foreground italic",
};

export function Highlighted({ text, kind }: { text: string; kind: BodyKind }) {
  const tokens =
    text.length > highlightLimit ? [{ kind: "plain" as const, text }] : tokenize(text, kind);
  return (
    <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 p-3 text-xs leading-relaxed break-all whitespace-pre-wrap">
      {tokens.map((t, i) => (
        <span key={i} className={colour[t.kind]}>
          <Reveal text={t.text} />
        </span>
      ))}
    </pre>
  );
}
