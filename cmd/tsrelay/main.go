// Command tsrelay delivers callbacks from a tailnet to developer machines.
package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"charm.land/fang/v2"
	"tailscale.com/ipn/ipnstate"
)

// deps holds what tests replace; zero values mean the real implementations.
type deps struct {
	HTTP      *http.Client
	Status    func(context.Context) (*ipnstate.Status, error)
	StatePath string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, deps{}))
}

// run executes tsrelay with args and returns its exit status.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	c := &cli{d: d, stdout: stdout, stderr: stderr, args: args}
	root := newRoot(c)
	root.SetArgs(args)
	return exitStatus(fang.Execute(ctx, root,
		fang.WithVersion(version),
		fang.WithoutManpage(),
		fang.WithErrorHandler(c.handleError),
	))
}
