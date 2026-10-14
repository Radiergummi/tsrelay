import type { Hook } from "@/api/schemas";

export function TunnelText({ tunnel }: { tunnel: Hook["tunnel"] }) {
  if (!tunnel) return <span className="text-muted-foreground">not attached</span>;
  return (
    <span className="whitespace-nowrap">
      {tunnel.node}
      {tunnel.target && (
        <>
          {" → "}
          <code className="text-xs">{tunnel.target}</code>
        </>
      )}
    </span>
  );
}
