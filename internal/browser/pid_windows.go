package browser

// Windows is unsupported; never reclaim another process's session.
func pidAlive(_ int) bool { return true }
