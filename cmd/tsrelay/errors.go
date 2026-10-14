package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// usageError is a mistake in how tsrelay was called: an unknown flag, a
// bad value, a missing argument. It exits with status 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return &usageError{fmt.Sprintf(format, a...)} }

// exitCodeError ends a command with that exit status. Whatever went wrong
// has been reported already, so it is never printed.
type exitCodeError int

func (e exitCodeError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// exitStatus maps what a command returned to tsrelay's exit status.
func exitStatus(err error) int {
	var ue *usageError
	var ec exitCodeError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ec):
		return int(ec)
	case errors.As(err, &ue):
		return 2
	default:
		return 1
	}
}

// usageArgs makes an argument validator's errors usage errors.
func usageArgs(v cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := v(cmd, args); err != nil {
			return &usageError{err.Error()}
		}
		return nil
	}
}
