# Performance baseline

Reference numbers for comparing changes on the request path. The raw benchmark output is [baseline.txt](baseline.txt), which is the input to benchstat.

## Environment

- **Machine:** Apple M3 Pro (12 cores), 36 GiB RAM, macOS
- **Go:** 1.27.1, darwin/arm64
- **Commits:**
  - benchmarks: `e4931de`
  - load runs: `103533a`; the harness changed between the two, the product code did not
- **Conditions:** nothing else running
- **Binary size** (stripped, `-trimpath -ldflags='-s -w'`): 27.07 MB on main (`3d58c93`) vs 33.59 MB with telemetry (`8596123`), +6.5 MB (+24%), largely `google.golang.org/grpc` and `genproto` linked through the otlp*http exporters' internals although only HTTP export is used.

Everything runs in-process over loopback: sender, relay, tunnel, CLI forwarder and app. These numbers measure tsrelay's own cost, not tsnet or WireGuard.

## How to compare

    go test -run='^$' -bench=. -count=6 -p 1 ./internal/server ./internal/tunnel/... ./internal/inspect > new.txt
    go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e docs/perf/baseline.txt new.txt

## Benchmarks (`-count=6`)

```
pkg: github.com/Radiergummi/tsrelay/internal/inspect
cpu: Apple M3 Pro
                    │ docs/perf/baseline.txt │
                    │         sec/op         │
Redact-12                       440.5n ± 71%
Capture/bodies-12               11.18µ ± 18%
Capture/noBodies-12             303.7n ± 85%
geomean                         1.143µ

                    │ docs/perf/baseline.txt │
                    │          B/op          │
Redact-12                         528.0 ± 0%
Capture/bodies-12               176.1Ki ± 0%
Capture/noBodies-12               64.00 ± 0%
geomean                         1.784Ki

                    │ docs/perf/baseline.txt │
                    │       allocs/op        │
Redact-12                         9.000 ± 0%
Capture/bodies-12                 4.000 ± 0%
Capture/noBodies-12               1.000 ± 0%
geomean                           3.302

                    │ docs/perf/baseline.txt │
                    │          B/s           │
Capture/bodies-12              87.41Gi ± 15%
Capture/noBodies-12            3.190Ti ± 47%
geomean                        534.4Gi

pkg: github.com/Radiergummi/tsrelay/internal/server
                                 │ docs/perf/baseline.txt │
                                 │         sec/op         │
Call/tracing=false/empty-12                  189.8µ ±  6%
Call/tracing=false/1KiB-12                   195.1µ ±  5%
Call/tracing=false/1MiB-12                   771.2µ ±  9%
Call/tracing=false/stream1MiB-12             912.1µ ± 29%
Call/tracing=true/empty-12                   194.4µ ± 65%
Call/tracing=true/1KiB-12                    196.4µ ±  4%
Call/tracing=true/1MiB-12                    711.7µ ±  8%
Call/tracing=true/stream1MiB-12              911.2µ ±  9%
Direct/empty-12                              36.43µ ±  9%
Direct/1KiB-12                               37.82µ ±  1%
Direct/1MiB-12                               282.8µ ± 39%
Direct/stream1MiB-12                         237.4µ ± 24%
geomean                                      250.0µ

                                 │ docs/perf/baseline.txt │
                                 │          B/op          │
Call/tracing=false/empty-12                 102.6Ki ±  4%
Call/tracing=false/1KiB-12                  108.9Ki ±  4%
Call/tracing=false/1MiB-12                  335.2Ki ± 11%
Call/tracing=false/stream1MiB-12            284.3Ki ±  1%
Call/tracing=true/empty-12                  105.7Ki ±  2%
Call/tracing=true/1KiB-12                   113.4Ki ±  2%
Call/tracing=true/1MiB-12                   341.1Ki ±  7%
Call/tracing=true/stream1MiB-12             291.6Ki ±  1%
Direct/empty-12                             5.554Ki ±  3%
Direct/1KiB-12                              6.361Ki ±  1%
Direct/1MiB-12                              39.29Ki ±  1%
Direct/stream1MiB-12                        6.416Ki ±  1%
geomean                                     68.83Ki

                                 │ docs/perf/baseline.txt │
                                 │       allocs/op        │
Call/tracing=false/empty-12                    358.0 ± 1%
Call/tracing=false/1KiB-12                     387.0 ± 1%
Call/tracing=false/1MiB-12                     543.0 ± 1%
Call/tracing=false/stream1MiB-12               602.0 ± 1%
Call/tracing=true/empty-12                     393.0 ± 0%
Call/tracing=true/1KiB-12                      424.0 ± 0%
Call/tracing=true/1MiB-12                      584.0 ± 2%
Call/tracing=true/stream1MiB-12                640.5 ± 1%
Direct/empty-12                                64.00 ± 0%
Direct/1KiB-12                                 71.00 ± 0%
Direct/1MiB-12                                 73.00 ± 0%
Direct/stream1MiB-12                           90.00 ± 0%
geomean                                        257.3

                                 │ docs/perf/baseline.txt │
                                 │          B/s           │
Call/tracing=false/1KiB-12                  5.007Mi ±  5%
Call/tracing=false/1MiB-12                  1.266Gi ±  8%
Call/tracing=false/stream1MiB-12            1.075Gi ± 23%
Call/tracing=true/1KiB-12                   4.969Mi ±  3%
Call/tracing=true/1MiB-12                   1.372Gi ±  8%
Call/tracing=true/stream1MiB-12             1.072Gi ±  9%
Direct/1KiB-12                              25.82Mi ±  1%
Direct/1MiB-12                              3.527Gi ± 30%
Direct/stream1MiB-12                        4.114Gi ± 19%
geomean                                     303.0Mi

pkg: github.com/Radiergummi/tsrelay/internal/tunnel
             │ docs/perf/baseline.txt │
             │         sec/op         │
RoundTrip-12              44.13µ ± 3%

             │ docs/perf/baseline.txt │
             │          B/op          │
RoundTrip-12             5.041Ki ± 0%

             │ docs/perf/baseline.txt │
             │       allocs/op        │
RoundTrip-12               57.00 ± 0%
```

## Load (`TestLoad`, 10 s per pass)

Concurrency 2, plain, 1 KiB bodies:

```
mode=plain concurrency=2 duration=10s body=1024B GOMAXPROCS=12
            calls/s        p50        p95        p99  allocs/call    peak gr
direct        43071   39.625µs       91µs  146.084µs           81         23
relay          8214  168.833µs  491.292µs    1.108ms          401         40
added                129.208µs  400.292µs  961.916µs
```

Concurrency 16, plain, 1 KiB bodies. **This run fails**; see finding 1:

```
3915 calls to http://127.0.0.1:53879/~moritz/load failed
   3915  status 502
mode=plain concurrency=16 duration=10s body=1024B GOMAXPROCS=12
            calls/s        p50        p95        p99  allocs/call    peak gr
direct        88628  161.875µs  328.083µs  539.333µs           81         88
relay          3521  848.958µs    2.174ms 31.771416ms          475        192
added                687.083µs 1.845917ms 31.232083ms
```

Concurrency 16, streaming a 1 MiB response:

```
mode=streaming concurrency=16 duration=10s body=1024B GOMAXPROCS=12
            calls/s        p50        p95        p99  allocs/call    peak gr
direct         4141 3.849125ms 4.799833ms 5.743167ms          107         88
relay           987 15.615583ms 22.383792ms 25.516417ms          656        231
added               11.766458ms 17.583959ms 19.77325ms
```

## Findings

Each finding below was a candidate change for the maintainer to decide on. Findings 1 and 2 have since been implemented, and finding 3 was measured; each says what came of it. The benchmark and load numbers above are still those from before the changes.

1. **The CLI forwarder runs out of local ports under concurrent calls.** `client.NewForwarder` (`internal/client/forward.go`) clones `http.DefaultTransport`, which keeps at most 2 idle connections per host.
   - **What happens:** with more than two calls in flight, the forwarder opens a new connection to the local app for every extra call and closes it afterwards. Within seconds at 16 concurrent calls, the machine runs out of ephemeral ports. Every failure in the run above is a 502 `upstream_unreachable`, whose underlying error is `connect: can't assign requested address`.
   - **Experiment:** with `MaxIdleConnsPerHost = 64` (and `MaxIdleConns = 100`) set temporarily and then reverted, the same run went from about 3,500 calls/s with 3,255 to 5,418 failures and a p99 of 26 to 37 ms, to 21,324 calls/s with 0 failures and a p99 of 2.0 ms.
   - **Proposal:** raise the forwarder transport's idle limits, or tie them to `--tunnel-concurrency`.
   - **Done:** the forwarder now keeps an idle connection for every call in flight, since the relay's `--tunnel-concurrency` already bounds them; `TestForwarderReusesConnections` covers it. The concurrency-16 plain run now completes with no failures: 22,154 calls/s, p50 633 µs, p99 1.67 ms (relay row).
2. **Every response copy allocates a fresh 32 KiB buffer.** Neither reverse proxy sets `BufferPool`: not the relay's (`internal/relay/handler.go`) and not the CLI forwarder's (`internal/client/forward.go`).
   - **Cost:** `ReverseProxy.copyBuffer` accounts for 49% of all bytes allocated in the concurrency-2 profile. That is about 64 KiB of the roughly 105 KiB an empty call allocates (`BenchmarkCall/empty`, compared with 5.5 KiB for the direct call).
   - **Proposal:** a shared `sync.Pool` of 32 KiB buffers as `BufferPool` for both proxies. Re-measure `B/op` and GC time.
   - **Done:** both proxies share `tunnel.Buffers`. `BenchmarkCall` allocates 59 to 67% fewer bytes for empty and 1 KiB calls (empty: 101 KiB → 34 KiB) and about 21% fewer for 1 MiB calls. Time per call shows no significant change when the two builds are run interleaved.
3. **Throughput when streaming is about a quarter of the direct path.**
   - **Measured:** a 1 MiB streamed response takes 912 µs through the relay against 237 µs direct (`BenchmarkCall/stream1MiB`). Under load at concurrency 16 it reaches 987 calls/s against 4,141 calls/s direct, adding about 12 ms at p50.
   - **Where the time goes:** the CPU profile is dominated by system calls and scheduler wakeups. On the tunnel, HTTP/2 writes each frame with its own flush (`http2.(*ClientConn).writeHeaders` → `bufio.(*Writer).Flush` → `tunnel.(*watchedConn).Write`).
   - **Proposal:** investigate the frame size and write batching, and the HTTP/2 flow-control windows of the tunnel transport and server. Measure before changing anything; this finding is less certain than 1 and 2.
   - **Measured:** most of the gap is the number of hops, not the tunnel. A call through the relay passes two reverse proxies (the relay's and the CLI forwarder's), and two plain `httputil.ReverseProxy` hops over loopback, with no relay or tunnel, already take 680 µs (1 MiB body) and 717 µs (streamed) against 270 µs and 278 µs direct. Through the relay it is 815 µs and 1,099 µs, so the tunnel's HTTP/2 hop and the relay's own logic add about 20% (body) and 50% (streamed) on top of the hops themselves. Raising the HTTP/2 maximum frame size to 1 MiB on both tunnel ends made no measurable difference. No change was made; the remaining streaming overhead lies in the standard library's HTTP/2 framing and is not worth a custom transport for now.
4. **The relay adds about 130 µs at p50 per small call** (concurrency 2: 169 µs through the relay against 40 µs direct; 401 against 81 allocations per call, process-wide). Of the relay's own code, only `relay.(*Handler).ServeHTTP` shows up in the CPU profile, at under 1%; the rest is in `net/http`, HTTP/2 and system calls. Findings 1 to 3 are where the gains are, not the relay's own logic.
5. **Tracing overhead.**
   - **Allocations:** tracing adds about 35 allocations and 3 to 6 KiB per call (`BenchmarkCall/tracing=true` against `tracing=false`).
   - **Time:** the difference is within benchmark noise.
   - **With tracing off:** the span helpers and `forwardTransport` allocate nothing (`TestTracingOffAllocatesNothing`); no main-vs-branch end-to-end comparison was made.

## Open questions

- **A streamed read stalled once.** In the direct pass of one streaming run, a response body read never returned. That run followed a port-exhausting run, and the stall did not recur. The harness now bounds every call to 30 s and counts such a call as failed, but the cause is unknown.
- **No TIME_WAIT sockets were seen** in `netstat` during port exhaustion, probably because the sandboxed shell could not see the test process's sockets. This is unconfirmed.
