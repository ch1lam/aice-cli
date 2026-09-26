package guard

// SetDesktopEnabled is published with the application's tool/prompt snapshot,
// only while shared resources are idle. It is not an OS permission grant.
func (g *Guard) SetDesktopEnabled(enabled bool) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.desktopEnabled = enabled
	g.mu.Unlock()
}

func isDesktopTool(name string) bool {
	switch name {
	case "desktop_apps", "desktop_observe", "desktop_act":
		return true
	default:
		return false
	}
}
