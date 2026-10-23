package tui

import "testing"

func TestMenuListMovementAndWindow(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		got, want int
	}{
		{"wrap forward", wrapIndex(2, 1, 3), 0},
		{"wrap backward", wrapIndex(0, -1, 3), 2},
		{"wrap empty", wrapIndex(0, 1, 0), 0},
		{"clamp top", clampIndex(-1, 3), 0},
		{"clamp bottom", clampIndex(3, 3), 2},
		{"clamp empty", clampIndex(1, 0), 0},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
	if start, end := listWindow(1, 3, 10); start != 0 || end != 3 {
		t.Errorf("top window = [%d,%d)", start, end)
	}
	if start, end := listWindow(9, 3, 10); start != 7 || end != 10 {
		t.Errorf("bottom window = [%d,%d)", start, end)
	}
}
