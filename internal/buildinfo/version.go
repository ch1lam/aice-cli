// Package buildinfo owns the binary version shared by the UI and HTTP clients.
package buildinfo

import "runtime/debug"

// Version is set by the release build through -ldflags -X. Local builds use dev.
// It must not be changed at runtime.
var Version = "dev"

// UserAgent identifies AICE without including account or host information.
func UserAgent() string {
	return "aice/" + Version
}

// Build identifies the exact binary for records such as Session run
// configurations: the release version, or for local builds "dev+" and the VCS
// revision Go stamps into the binary, marked "-dirty" when the checkout had
// uncommitted changes.
func Build() string {
	info, ok := debug.ReadBuildInfo()
	if Version != "dev" || !ok {
		return Version
	}
	return describe(Version, info.Settings)
}

func describe(version string, settings []debug.BuildSetting) string {
	var revision, modified string
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return version
	}
	build := version + "+" + revision[:min(12, len(revision))]
	if modified == "true" {
		build += "-dirty"
	}
	return build
}
