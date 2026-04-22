package commands

import "github.com/pura-labs/cli/internal/auth"

// newTestStoreSeed writes a full credential record into the (already-
// isolated-HOME) store. Shared between doctor / auth tests.
func newTestStoreSeed(profile, apiURL, token string) error {
	return auth.NewStore().Save(profile, auth.Record{
		Token:  token,
		APIUrl: apiURL,
	})
}
