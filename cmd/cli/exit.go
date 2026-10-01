package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// exitError carries a process exit code other than 1. A nil err means the command already
// explained itself and only the code matters.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// Main runs the CLI and returns the process exit code.
func Main() int {
	return runMain(os.Args[1:], os.Stdout, os.Stderr)
}

func runMain(args []string, stdout, stderr io.Writer) int {
	root := NewRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintln(stderr, "Error:", ee.err)
		}
		return ee.code
	}
	fmt.Fprintln(stderr, "Error:", err)
	return 1
}
