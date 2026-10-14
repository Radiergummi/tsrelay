import type { JNode } from "./jsonParse";
import { Reveal } from "./Reveal";

/** JsonTree shows parsed JSON collapsibly. <details> needs no script and no state. */
export function JsonTree({ node }: { node: JNode }) {
  return (
    <div className="max-h-[60vh] overflow-auto rounded-md border bg-muted/40 p-3 font-mono text-xs leading-relaxed">
      <Node node={node} depth={0} />
    </div>
  );
}

function Node({ node, depth }: { node: JNode; depth: number }) {
  switch (node.type) {
    case "string":
      return (
        <span className="break-all text-status-2xx">
          <Reveal text={JSON.stringify(node.value)} />
        </span>
      );
    case "number":
      return <span className="text-status-3xx">{node.raw}</span>;
    case "literal":
      return <span className="text-status-4xx">{node.raw}</span>;
    case "array":
      if (node.items.length === 0) return <span>[]</span>;
      return (
        <details open={depth < 2} className="inline">
          <summary className="inline cursor-pointer text-muted-foreground">
            [ {node.items.length} items
          </summary>
          <ul className="border-l pl-4">
            {node.items.map((v, i) => (
              <li key={i}>
                <Node node={v} depth={depth + 1} />
              </li>
            ))}
          </ul>
          <span className="text-muted-foreground">]</span>
        </details>
      );
    case "object":
      if (node.entries.length === 0) return <span>{"{}"}</span>;
      return (
        <details open={depth < 2} className="inline">
          <summary className="inline cursor-pointer text-muted-foreground">
            {"{"} {node.entries.length} keys
          </summary>
          <ul className="border-l pl-4">
            {node.entries.map(([k, v], i) => (
              <li key={i}>
                <span className="text-primary">
                  <Reveal text={JSON.stringify(k)} />
                </span>
                <span className="text-muted-foreground">: </span>
                <Node node={v} depth={depth + 1} />
              </li>
            ))}
          </ul>
          <span className="text-muted-foreground">{"}"}</span>
        </details>
      );
  }
}
