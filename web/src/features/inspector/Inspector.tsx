import { lazy, Suspense, useMemo, useState } from "react";
import { Link } from "react-router";
import { ApiError } from "@/api/client";
import { useReplay } from "@/api/mutations";
import { useCall } from "@/api/queries";
import type { Call, Hook, Me } from "@/api/schemas";
import { PageError } from "@/components/PageState";
import { StatusText } from "@/components/StatusText";
import { TimeAgo } from "@/components/TimeAgo";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/ui/copy-button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { parseDuration, formatDuration } from "@/lib/duration";
import { absoluteTime, outcomeLabel, senderLabel } from "@/lib/format";
import { toast } from "@/lib/toast";
import { owns } from "@/lib/viewer";
import { Attempts } from "./Attempts";
import { BodyViewer } from "./BodyViewer";
import { decodeBody, headerValue } from "./bytes";
import { curlTargets } from "./curl";
import { HeaderTable } from "./HeaderTable";
import { Reveal } from "./Reveal";
import { useShortcuts } from "./shortcuts";

const DiffView = lazy(async () => ({ default: (await import("./DiffView")).DiffView }));

/** Inspector shows one call of hook. onSelect selects another call (the replay just made). */
export function Inspector({
  hook,
  me,
  callId,
  onSelect,
}: {
  hook: Hook;
  me: Me;
  callId: string;
  onSelect: (callId: string) => void;
}) {
  const call = useCall(hook.id, callId);
  if (call.isPending) return <Skeleton className="h-64 w-full" />;
  if (call.isError) {
    if (call.error instanceof ApiError && call.error.status === 404) {
      return (
        <div className="py-12 text-center">
          <p className="mb-2 font-medium">This call has expired from the inspector</p>
          <p className="text-muted-foreground">
            The relay keeps only the most recent calls of each hook, in memory, and forgets them
            when it restarts.
          </p>
        </div>
      );
    }
    return <PageError error={call.error} />;
  }
  return (
    <CallView
      key={call.data.id}
      hook={hook}
      call={call.data}
      owner={owns(me, hook)}
      onSelect={onSelect}
    />
  );
}

function CallView({
  hook,
  call,
  owner,
  onSelect,
}: {
  hook: Hook;
  call: Call;
  owner: boolean;
  onSelect: (callId: string) => void;
}) {
  // CallView is remounted by its key (call.id) whenever the call changes, so
  // this state always starts fresh on "request" without needing an effect.
  const [tab, setTab] = useState("request");
  const replay = useReplay(hook.id);
  const body = useMemo(() => decodeBody(call.requestBody), [call.requestBody]);
  // Narrowed to what the commands use: every call event replaces the hook (its lastCall).
  const { url, tunnel } = hook;
  const curls = useMemo(
    () => (owner ? curlTargets({ url, tunnel }, call, body) : []),
    [owner, url, tunnel, call, body],
  );
  const allowed = owner && call.replay?.allowed === true;
  const showDiff = call.kind === "replay" && !!call.replayOf && owner;
  const reason = call.replay?.reason ?? "";
  const last = call.attempts.at(-1);
  const ms = parseDuration(call.duration) ?? 0;
  const timeout = parseDuration(hook.effective.responseTimeout) ?? 0;

  const doReplay = () => {
    if (!allowed || replay.isPending) return;
    replay.mutate(call.id, {
      onSuccess: (c) => {
        toast.success(`Replayed: ${outcomeLabel(c)}`);
        onSelect(c.id);
      },
    });
  };
  const copyCurl = (i = 0) => {
    if (!allowed || !curls[i]) return;
    navigator.clipboard.writeText(curls[i].command).then(
      () => toast.success(`Copied curl to the ${curls[i]!.label}`),
      () => {},
    );
  };
  useShortcuts({ r: doReplay, c: () => copyCurl(0) }, owner);

  return (
    <article className="space-y-4">
      <header className="space-y-2">
        <h2 className="font-mono text-base font-medium break-all">
          {call.method} <Reveal text={call.path} />
          {call.query && (
            <span className="text-muted-foreground">
              ?<Reveal text={call.query} />
            </span>
          )}
        </h2>
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          <StatusText call={call} />
          {call.outcome !== "pending" && (
            <span className="flex items-center gap-2">
              {formatDuration(ms)}
              {timeout > 0 && (
                <span
                  className="h-1 w-20 overflow-hidden rounded-full bg-muted"
                  title={`of the ${hook.effective.responseTimeout} response timeout`}
                >
                  <span
                    className="block h-full bg-primary"
                    style={{ width: `${Math.min(100, (ms / timeout) * 100)}%` }}
                  />
                </span>
              )}
            </span>
          )}
          {call.kind === "test" && (
            <span className="rounded-sm bg-muted px-1.5 text-xs">test request</span>
          )}
          {call.kind === "replay" && call.replayOf && (
            <span className="text-muted-foreground">
              replay of{" "}
              <Link
                to={`/hooks/${encodeURIComponent(hook.id)}/calls/${encodeURIComponent(call.replayOf)}`}
                className="font-mono text-primary hover:underline"
              >
                {call.replayOf}
              </Link>
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>
            from {senderLabel(call.sender)}
            {call.sender.node && call.sender.tags?.length ? ` (${call.sender.node})` : ""}
          </span>
          <span>
            {absoluteTime(call.receivedAt)} · <TimeAgo iso={call.receivedAt} />
          </span>
          <span className="group flex items-center font-mono">
            {call.id}
            <CopyButton text={call.id} label="call ID" onHover />
          </span>
        </div>
        {owner ? (
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" disabled={!allowed || replay.isPending} onClick={doReplay}>
              Replay
            </Button>
            <Button size="sm" variant="outline" disabled={!allowed} onClick={() => copyCurl(0)}>
              Copy as curl
            </Button>
            {curls.length > 1 && (
              <Button size="sm" variant="ghost" disabled={!allowed} onClick={() => copyCurl(1)}>
                …to the hook URL
              </Button>
            )}
            {!allowed && reason && <span className="text-xs text-muted-foreground">{reason}</span>}
          </div>
        ) : (
          <p className="text-muted-foreground">
            You see this call as metadata only: only the hook's owner sees its query, headers and
            bodies.
          </p>
        )}
      </header>

      <Tabs value={tab} onValueChange={(v: string) => setTab(v)}>
        <TabsList>
          <TabsTrigger value="request">Request</TabsTrigger>
          <TabsTrigger value="response">Response</TabsTrigger>
          <TabsTrigger value="attempts">Attempts</TabsTrigger>
          {showDiff && <TabsTrigger value="diff">Diff</TabsTrigger>}
        </TabsList>
        <TabsContent value="request" className="space-y-4 pt-3">
          {owner && call.query && (
            <QueryTable query={call.query} truncated={call.queryTruncated === true} />
          )}
          <HeaderTable
            headers={call.requestHeaders}
            redacted={call.redacted}
            metadataOnly={!owner}
          />
          <BodyViewer
            capture={call.requestBody}
            contentType={headerValue(call.requestHeaders, "Content-Type")}
            metadataOnly={!owner}
            label="request body"
          />
        </TabsContent>
        <TabsContent value="response" className="space-y-4 pt-3">
          {last ? (
            <>
              <HeaderTable
                headers={last.responseHeaders}
                redacted={last.redacted}
                metadataOnly={!owner}
              />
              <BodyViewer
                capture={last.responseBody}
                contentType={headerValue(last.responseHeaders, "Content-Type")}
                metadataOnly={!owner}
                label="response body"
              />
            </>
          ) : (
            <p className="text-muted-foreground">No response: the call was never delivered.</p>
          )}
        </TabsContent>
        <TabsContent value="attempts" className="pt-3">
          <Attempts attempts={call.attempts} />
        </TabsContent>
        {showDiff && (
          <TabsContent value="diff" className="pt-3">
            <Suspense fallback={<Skeleton className="h-24 w-full" />}>
              <DiffView call={call} />
            </Suspense>
          </TabsContent>
        )}
      </Tabs>
    </article>
  );
}

function QueryTable({ query, truncated }: { query: string; truncated: boolean }) {
  const params = [...new URLSearchParams(query)];
  return (
    <div>
      <table className="w-full text-xs">
        <tbody>
          {params.map(([k, v], i) => (
            <tr key={i} className="border-b last:border-0">
              <th className="w-1/3 py-1 pr-3 text-left font-mono font-medium break-all">
                <Reveal text={k} />
              </th>
              <td className="py-1 font-mono break-all">
                <Reveal text={v} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {truncated && (
        <p className="mt-1 text-xs text-muted-foreground">
          The query string was not captured in full.
        </p>
      )}
    </div>
  );
}
