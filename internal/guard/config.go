package guard

// Config is constructor-supplied engine configuration. Policies are fixed;
// the application supplies only directories discovered at startup.
type Config struct {
	// ReadOnlyRoots are extra directories whose descendants skip the
	// outside-workspace ask for read-class tools (read, grep, find, ls).
	// Write-class tools are not granted. Callers pass discovered Skill
	// directories; this package does not discover them.
	ReadOnlyRoots []string
}
