// Command tsrelay delivers callbacks from a tailnet to developer machines.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"charm.land/fang/v2"
	"tailscale.com/ipn/ipnstate"
)

// deps holds what tests replace; zero values mean the real implementations.
type deps struct {
	HTTP       *http.Client
	Status     func(context.Context) (*ipnstate.Status, error)
	StatePath  string
	Stdin      io.Reader // what questions are answered on
	Executable string    // what tsrelay update replaces
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{}))
}

// run executes tsrelay with args and returns its exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	c := &cli{d: d, stdout: stdout, stderr: stderr, args: args}
	if id := os.Getenv(backgroundIDEnv); id != "" {
		// A background tunnel: stderr is its log, and stdout the pipe to the
		// tsrelay that started it (see startBackground).
		c.bg, c.stdout = &bgChild{id: id, pipe: stdout}, stderr
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		if err := watchStop(id, cancel); err != nil {
			fmt.Fprintln(stderr, "tsrelay stop can only kill this tunnel:", err)
		}
	}
	if runtime.GOOS == "windows" {
		if exe, err := executablePath(d); err == nil {
			removeOldExecutable(exe)
		}
	}
	root := newRoot(c)
	root.SetArgs(args)
	err := fang.Execute(ctx, root,
		fang.WithVersion(version),
		fang.WithoutManpage(),
		fang.WithErrorHandler(c.handleError),
	)
	c.offer(ctx)
	return exitStatus(err)
}
