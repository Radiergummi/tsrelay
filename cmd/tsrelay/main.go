// Command tsrelay delivers callbacks from a tailnet to developer machines.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"tailscale.com/ipn/ipnstate"
)

const usage = `tsrelay delivers callbacks from your tailnet to apps on your machine.

Usage:
  tsrelay http <port|url> [flags] [-- cmd args...]  forward a hook to a local app
  tsrelay hooks ls|create|update|rm                 manage your hooks
  tsrelay url [<port|url>]                          print the URL tsrelay http would use
  tsrelay serve [flags]                             run the relay (as its own tailnet node)

Run "tsrelay <command> --help" for the flags of a command.
`

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

func run(ctx context.Context, args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve(ctx, args[1:], stderr)
	case "http":
		return httpCmd(ctx, args[1:], stdout, stderr, d)
	case "hooks":
		return hooksCmd(ctx, args[1:], stdout, stderr, d)
	case "url":
		return urlCmd(ctx, args[1:], stdout, stderr, d)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "tsrelay: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
