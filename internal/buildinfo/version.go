// Package buildinfo owns the binary version shared by the UI and HTTP clients.
package buildinfo

// Version is set by the release build through -ldflags -X. Local builds use dev.
// It must not be changed at runtime.
var Version = "dev"

// UserAgent identifies AICE without including account or host information.
func UserAgent() string {
	return "aice/" + Version
}
