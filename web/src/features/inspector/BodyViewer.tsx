import { lazy, memo, Suspense, useMemo, useState } from "react";
import { Link } from "react-router";
import { paths } from "@/api/client";
import type { Capture } from "@/api/schemas";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Skeleton } from "@/components/ui/skeleton";
import { formatSize } from "@/lib/format";
import { bodyKind, decodeBody } from "./bytes";
import { highlightLimit, Highlighted } from "./Highlighted";
import { parseJSON } from "./jsonParse";

const JsonTree = lazy(async () => ({ default: (await import("./JsonTree")).JsonTree }));
const HexView = lazy(async () => ({ default: (await import("./HexView")).HexView }));

type View = "tree" | "raw" | "hex" | "text";

/**
 * BodyViewer shows one captured body, or says why there is none to show.
 * Memoized: its props are stable while the inspector re-renders on every
 * call event (the hook's lastCall), and a body can be 64 KiB of tokens.
 */
export const BodyViewer = memo(function BodyViewer({
  capture,
  contentType,
  metadataOnly,
  relayNoBodies = false,
  label,
  hookId,
}: {
  capture: Capture;
  contentType: string | undefined;
  metadataOnly: boolean;
  /** The relay keeps no bodies at all (--capture-bodies=false), so the hook's settings can't help. */
  relayNoBodies?: boolean;
  label: string;
  hookId: string;
}) {
  if (capture.size === 0 && capture.truncated)
    return <Note>The body was not read: the call was refused before delivery.</Note>;
  if (capture.size === 0) return <Note>No body.</Note>;
  if (metadataOnly)
    return (
      <Note>{formatSize(capture.size)} body, not shown: only the hook's owner sees bodies.</Note>
    );
  if (!capture.captured && relayNoBodies)
    return <Note>{formatSize(capture.size)} body. This relay doesn't capture bodies.</Note>;
  if (!capture.captured)
    return (
      <Note>
        {formatSize(capture.size)} body. Bodies aren't captured for this hook.{" "}
        <Link to={`${paths.hook(hookId)}/edit`} className="text-primary hover:underline">
          Edit the hook
        </Link>{" "}
        to capture them.
      </Note>
    );
  return (
    <Captured
      key={`${capture.body ?? ""}:${capture.size}`}
      capture={capture}
      contentType={contentType}
      label={label}
    />
  );
});

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
  // A huge valid JSON document would still mean hundreds of thousands of tree
  // nodes; above highlightLimit (where Highlighted also gives up on tokens)
  // it's shown raw instead of rendering a tree that would freeze the tab.
  const tooLargeForTree = body.text !== null && body.text.length > highlightLimit;
  const tree = useMemo(
    () =>
      kind === "json" && body.text !== null && !body.hasNul && !tooLargeForTree
        ? parseJSON(body.text)
        : null,
    [kind, body, tooLargeForTree],
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
    <div className="group space-y-2">
      <div className="flex items-center gap-3 text-xs text-muted-foreground">
        <span>
          {formatSize(capture.size)}
          {capture.truncated && `, showing the first ${formatSize(body.bytes.length)}`}
        </span>
        {kind === "json" && !tree && !body.binary && (
          <span>
            {capture.truncated
              ? "cut at the capture limit, so shown raw"
              : tooLargeForTree
                ? "too large to show as a tree, so shown raw"
                : "not valid JSON, so shown raw"}
          </span>
        )}
        <span className="ml-auto flex items-center gap-1">
          {views
            .filter((v) => v !== view)
            .map((v) => (
              <Button key={v} variant="ghost" size="xs" onClick={() => setView(v)}>
                {names[v]}
              </Button>
            ))}
          {body.text !== null && <CopyButton text={body.text} label={label} onHover />}
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
