package deps

// Browser installation is disabled on Windows. Never reclaim a lock there.
func reclaimBrowserInstallLock(_ string) (bool, error) { return false, nil }
