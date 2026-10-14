import { lazy, Suspense, useMemo, useState } from "react";
import type { Capture } from "@/api/schemas";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatSize } from "@/lib/format";
import { bodyKind, decodeBody } from "./bytes";
import { Highlighted } from "./Highlighted";
import { parseJSON } from "./jsonParse";

const JsonTree = lazy(async () => ({ default: (await import("./JsonTree")).JsonTree }));
const HexView = lazy(async () => ({ default: (await import("./HexView")).HexView }));

type View = "tree" | "raw" | "hex" | "text";

/** BodyViewer shows one captured body, or says why there is none to show. */
export function BodyViewer({
  capture,
  contentType,
  metadataOnly,
  label,
}: {
  capture: Capture;
  contentType: string | undefined;
  metadataOnly: boolean;
  label: string;
}) {
  if (capture.size === 0 && capture.truncated)
    return <Note>The body was not read: the call was refused before delivery.</Note>;
  if (capture.size === 0) return <Note>No body.</Note>;
  if (metadataOnly)
    return (
      <Note>{formatSize(capture.size)} body, not shown: only the hook's owner sees bodies.</Note>
    );
  if (!capture.captured)
    return (
      <Note>
        {formatSize(capture.size)} body, not captured: body capture is off for this hook or the
        relay.
      </Note>
    );
  return <Captured capture={capture} contentType={contentType} label={label} />;
}

function Captured({
  capture,
  contentType,
  label,
}: {
  capture: Capture;
  contentType: string | undefined;
  label: string;
}) {
  const body = useMemo(() => decodeBody(capture), [capture]);
  const kind = bodyKind(contentType);
  const tree = useMemo(
    () => (kind === "json" && body.text !== null && !body.hasNul ? parseJSON(body.text) : null),
    [kind, body],
  );
  const views: View[] = body.binary
    ? body.text !== null
      ? ["hex", "text"]
      : ["hex"]
    : tree
      ? ["tree", "raw"]
      : ["raw"];
  const [view, setView] = useState<View>(views[0]!);
  const names: Record<View, string> = {
    tree: "Tree",
    raw: "Raw",
    hex: "Hex",
    text: "Show as text",
  };
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span>
          {formatSize(capture.size)}
          {capture.truncated && `, showing the first ${formatSize(body.bytes.length)}`}
        </span>
        {kind === "json" && !tree && !body.binary && (
          <span>
            {capture.truncated
              ? "cut at the capture limit, so shown raw"
              : "not valid JSON, so shown raw"}
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          {views.length > 1 &&
            views
              .filter((v) => v !== view)
              .map((v) => (
                <Button key={v} variant="ghost" size="xs" onClick={() => setView(v)}>
                  {names[v]}
                </Button>
              ))}
          {body.text !== null && <CopyButton text={body.text} label={label} />}
        </span>
      </div>
      <Suspense fallback={<Skeleton className="h-24 w-full" />}>
        {view === "tree" && tree && <JsonTree node={tree} />}
        {view === "raw" && <Highlighted text={body.text ?? ""} kind={kind} />}
        {view === "text" && <Highlighted text={body.text ?? ""} kind="text" />}
        {view === "hex" && <HexView bytes={body.bytes} />}
      </Suspense>
    </div>
  );
}

function Note({ children }: { children: React.ReactNode }) {
  return <p className="text-muted-foreground">{children}</p>;
}
