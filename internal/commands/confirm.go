package commands

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/pura-labs/cli/internal/output"
)

// confirmMutation enforces an explicit confirmation step before mutating
// commands proceed. In non-interactive mode callers must pass the bypass flag
// (for example --yes or --force); otherwise the command should fail instead of
// silently no-oping.
func confirmMutation(w *output.Writer, bypass bool, bypassFlag, title, description, affirmative string) (bool, error) {
	if bypass {
		return true, nil
	}
	if !w.IsTTY {
		return false, fmt.Errorf("confirmation required; re-run with %s", bypassFlag)
	}

	var ok bool
	err := huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative(affirmative).
		Negative("Cancel").
		Value(&ok).
		Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}
		return false, err
	}
	return ok, nil
}
