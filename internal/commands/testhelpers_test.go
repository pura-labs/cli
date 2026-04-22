package commands

import "testing"

// runCmd executes the CLI with the given args against the singleton rootCmd,
// capturing stdout and stderr and returning just stdout (envelope) + err.
// Shared by every *_test.go under this package — keeps the per-command
// tests focused on assertions, not plumbing.
func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := rootCmd
	cmd.SetArgs(args)
	var stdoutOut string
	var execErr error
	stdoutOut = captureStdout(t, func() {
		_ = captureStderr(t, func() {
			execErr = cmd.Execute()
		})
	})
	return stdoutOut, execErr
}
