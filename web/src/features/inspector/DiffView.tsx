import { memo } from "react";
import { useCall } from "@/api/queries";
import type { Attempt, Call } from "@/api/schemas";
import { QueryView } from "@/components/QueryView";
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
    ...headerLines(last?.responseHeaders).map(([name, value]) => `${name}: ${value}`),
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
function pretty(node: JNode, indent = ""): string {
  const inner = indent + "  ";
  switch (node.type) {
    case "string":
      return JSON.stringify(node.value);
    case "number":
    case "literal":
      return node.raw;
    case "array":
      return node.items.length
        ? `[\n${node.items.map((item) => inner + pretty(item, inner)).join(",\n")}\n${indent}]`
        : "[]";
    case "object":
      return node.entries.length
        ? `{\n${node.entries.map(([key, value]) => `${inner}${JSON.stringify(key)}: ${pretty(value, inner)}`).join(",\n")}\n${indent}}`
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
  const originalQuery = useCall(call.hookId, call.replayOf);
  return (
    <QueryView
      query={originalQuery}
      pending={<Skeleton className="h-24 w-full" />}
      gone={
        <p className="text-muted-foreground">
          The original call ({call.replayOf}) has expired, so there is nothing to compare with.
        </p>
      }
    >
      {(original) => <Diff original={original} call={call} />}
    </QueryView>
  );
});

function Diff({ original, call }: { original: Call; call: Call }) {
  const lines = diffLines(responseLines(original), responseLines(call));
  if (!lines)
    return <p className="text-muted-foreground">The responses are too large to compare here.</p>;
  return (
    <div>
      <p className="mb-2 text-xs text-muted-foreground">
        <span className="text-status-5xx">−</span> the original ({call.replayOf}),{" "}
        <span className="text-status-2xx">+</span> this replay
      </p>
      <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 py-2 text-xs leading-relaxed">
        {lines.map(({ op, text }, i) => (
          <div key={i} data-op={op} className={`px-3 break-all whitespace-pre-wrap ${style[op]}`}>
            {mark[op]}
            <Reveal text={text} />
          </div>
        ))}
      </pre>
    </div>
  );
}
