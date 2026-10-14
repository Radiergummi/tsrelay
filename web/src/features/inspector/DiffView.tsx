import { memo } from "react";
import { useCall } from "@/api/queries";
import type { Attempt, Call } from "@/api/schemas";
import { Skeleton } from "@/components/ui/skeleton";
import { outcomeLabel } from "@/lib/format";
import { bodyKind, decodeBody, headerValue } from "./bytes";
import { headerLines } from "./curl";
import { diffLines } from "./diff";
import { parseJSON, type JNode } from "./jsonParse";
import { Reveal } from "./Reveal";

/** responseLines is what a sender saw: the status, the headers, and the body (indented when it's JSON). */
function responseLines(call: Call): string[] {
  const last: Attempt | undefined = call.attempts.at(-1);
  const lines = [
    outcomeLabel(call),
    ...headerLines(last?.responseHeaders).map(([k, v]) => `${k}: ${v}`),
    "",
  ];
  if (!last) return lines;
  const body = decodeBody(last.responseBody);
  if (body.binary) return [...lines, `(${body.bytes.length} bytes of binary data)`];
  const text = body.text ?? "";
  const tree =
    bodyKind(headerValue(last.responseHeaders, "Content-Type")) === "json" ? parseJSON(text) : null;
  return [...lines, ...(tree ? pretty(tree) : text).split("\n")];
}

/** pretty indents parsed JSON, keeping number literals and duplicate keys as captured. */
function pretty(n: JNode, indent = ""): string {
  const inner = indent + "  ";
  switch (n.type) {
    case "string":
      return JSON.stringify(n.value);
    case "number":
    case "literal":
      return n.raw;
    case "array":
      return n.items.length
        ? `[\n${n.items.map((v) => inner + pretty(v, inner)).join(",\n")}\n${indent}]`
        : "[]";
    case "object":
      return n.entries.length
        ? `{\n${n.entries.map(([k, v]) => `${inner}${JSON.stringify(k)}: ${pretty(v, inner)}`).join(",\n")}\n${indent}}`
        : "{}";
  }
}

const style = {
  same: "",
  add: "bg-status-2xx/10 text-status-2xx",
  del: "bg-status-5xx/10 text-status-5xx",
};
const mark = { same: "  ", add: "+ ", del: "- " };

/**
 * DiffView compares a replay's response with its original's, line by line.
 * Memoized, like BodyViewer: call is stable across the inspector's re-renders.
 */
export const DiffView = memo(function DiffView({ call }: { call: Call }) {
  const original = useCall(call.hookId, call.replayOf);
  if (original.isPending) return <Skeleton className="h-24 w-full" />;
  if (original.isError)
    return (
      <p className="text-muted-foreground">
        The original call ({call.replayOf}) has expired, so there is nothing to compare with.
      </p>
    );
  const lines = diffLines(responseLines(original.data), responseLines(call));
  if (!lines)
    return <p className="text-muted-foreground">The responses are too large to compare here.</p>;
  return (
    <div>
      <p className="mb-2 text-xs text-muted-foreground">
        <span className="text-status-5xx">−</span> the original ({call.replayOf}),{" "}
        <span className="text-status-2xx">+</span> this replay
      </p>
      <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 py-2 text-xs leading-relaxed">
        {lines.map((l, i) => (
          <div
            key={i}
            data-op={l.op}
            className={`px-3 break-all whitespace-pre-wrap ${style[l.op]}`}
          >
            {mark[l.op]}
            <Reveal text={l.text} />
          </div>
        ))}
      </pre>
    </div>
  );
});
