package guard

// Config is constructor-supplied engine configuration. Policies are fixed;
// the application supplies only paths it owns or discovered at startup.
type Config struct {
	// ReadOnlyRoots are extra directories whose descendants skip the
	// outside-workspace ask for reads. Writes are not granted. Callers pass
	// discovered Skill directories; this package does not discover them.
	ReadOnlyRoots []string
	// CredentialFiles are application credential stores, denied like the
	// built-in host credential locations.
	CredentialFiles []string
	// ControlFiles configure AICE itself. Every write asks, regardless of
	// workspace location or Session grants.
	ControlFiles []string
}
