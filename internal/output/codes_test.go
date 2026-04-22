package output

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/pura-labs/cli/internal/api"
)

func TestExitCodeFor_Nil(t *testing.T) {
	if got := ExitCodeFor(nil); got != ExitOK {
		t.Errorf("nil error = %d, want %d", got, ExitOK)
	}
}

func TestExitCodeFor_TypedAPIErrors(t *testing.T) {
	cases := []struct {
		status int
		want   int
	}{
		{400, ExitInvalid},
		{401, ExitAuth},
		{403, ExitForbidden},
		{404, ExitNotFound},
		{409, ExitConflict},
		{429, ExitRateLimit},
		{500, ExitAPI},
		{502, ExitAPI},
		{0, ExitAPI},       // envelope-level ok=false with no HTTP status
		{418, ExitGeneric}, // 4xx we don't model → Generic, never silently API
	}
	for _, tc := range cases {
		err := &api.Error{Status: tc.status, Code: "x", Message: "m"}
		got := ExitCodeFor(err)
		if got != tc.want {
			t.Errorf("status=%d → exit %d, want %d", tc.status, got, tc.want)
		}
	}
}

func TestExitCodeFor_UnwrapsFromWrappedError(t *testing.T) {
	// Code paths often return `fmt.Errorf("context: %w", apiErr)` — make
	// sure our classifier still finds the *api.Error underneath.
	wrapped := fmt.Errorf("while doing X: %w", &api.Error{Status: 401, Code: "unauthorized"})
	if got := ExitCodeFor(wrapped); got != ExitAuth {
		t.Errorf("wrapped 401 = %d, want %d", got, ExitAuth)
	}
}

// fakeNetErr satisfies net.Error and is used to test the network branch.
type fakeNetErr struct{ msg string }

func (e *fakeNetErr) Error() string   { return e.msg }
func (e *fakeNetErr) Timeout() bool   { return true }
func (e *fakeNetErr) Temporary() bool { return true }

var _ net.Error = (*fakeNetErr)(nil)

func TestExitCodeFor_NetworkErrorIsAPI(t *testing.T) {
	err := &fakeNetErr{msg: "dial tcp: lookup pura.so: no such host"}
	if got := ExitCodeFor(err); got != ExitAPI {
		t.Errorf("net.Error = %d, want %d", got, ExitAPI)
	}
}

func TestExitCodeFor_UnknownErrorIsGeneric(t *testing.T) {
	err := errors.New("random file I/O failure")
	if got := ExitCodeFor(err); got != ExitGeneric {
		t.Errorf("plain error = %d, want %d", got, ExitGeneric)
	}
}
