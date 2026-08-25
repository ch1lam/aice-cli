package app

import (
	"fmt"
	"io"

	"github.com/ch1lam/aice-cli/internal/update"
)

func printUpdateCheck(output io.Writer, result update.CheckResult) error {
	if result.Available {
		_, err := fmt.Fprintf(
			output,
			"update available: %s -> %s (run `aice update`)\n",
			result.Current,
			result.Latest,
		)
		return err
	}
	if result.Current == result.Latest {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Latest)
		return err
	}
	// The current version cannot be compared (for example a dev build), so the
	// latest release is reported without claiming the install is current.
	_, err := fmt.Fprintf(output, "latest release is %s\n", result.Latest)
	return err
}

func printUpdateResult(output io.Writer, result update.UpdateResult) error {
	if !result.Updated {
		_, err := fmt.Fprintf(output, "aice is up to date (%s)\n", result.Latest)
		return err
	}
	if _, err := fmt.Fprintf(
		output,
		"updated aice %s -> %s\n",
		result.Current,
		result.Latest,
	); err != nil {
		return err
	}
	_, err := fmt.Fprintf(output, "restart aice to use the new version\n")
	return err
}
