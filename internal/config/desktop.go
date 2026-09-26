package config

import "strings"

// DesktopControlMode is an explicit user choice frozen for the next agent run.
type DesktopControlMode string

const (
	DesktopBackgroundOnly    DesktopControlMode = "background_only"
	DesktopForegroundAllowed DesktopControlMode = "foreground_allowed"
)

// filterSettingSources runs before both Viper merging and source projection.
// Trusted projects can select project preferences, not desktop authority.
func filterSettingSources(values map[string]any, source string) {
	if source == "default" || source == "user-settings" {
		return
	}
	for _, def := range SettingDefinitions() {
		if !def.UserOnly {
			continue
		}
		for key := range values {
			if strings.EqualFold(key, string(def.ID)) {
				delete(values, key)
			}
		}
	}
}
