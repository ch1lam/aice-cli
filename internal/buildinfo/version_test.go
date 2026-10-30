package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestDescribeNamesTheRevision(t *testing.T) {
	t.Parallel()
	revision := debug.BuildSetting{Key: "vcs.revision", Value: "0123456789abcdef0123"}
	for _, tc := range []struct {
		settings []debug.BuildSetting
		want     string
	}{
		{nil, "dev"},
		{[]debug.BuildSetting{revision, {Key: "vcs.modified", Value: "false"}}, "dev+0123456789ab"},
		{[]debug.BuildSetting{revision, {Key: "vcs.modified", Value: "true"}}, "dev+0123456789ab-dirty"},
	} {
		if got := describe("dev", tc.settings); got != tc.want {
			t.Errorf("describe(%v) = %q, want %q", tc.settings, got, tc.want)
		}
	}
}
