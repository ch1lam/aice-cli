package app

import (
	"fmt"
	"os"
	"testing"
)

// Default application tests use local providers and helpers, never downloads.
func TestMain(m *testing.M) {
	if err := os.Setenv("AICE_NO_DEP_INSTALL", "1"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
