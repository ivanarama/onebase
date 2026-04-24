//go:build !windows

package cli

import (
	"fmt"
	"os"
)

func showError(msg string) {
	fmt.Fprintln(os.Stderr, msg)
}
