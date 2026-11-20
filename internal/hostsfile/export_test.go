package hostsfile

// SetSyncDir replaces the function Save syncs the hosts directory with, and
// restores the previous one when the returned function is called.
func SetSyncDir(fn func(dir string) error) (restore func()) {
	previous := syncDir
	syncDir = fn
	return func() { syncDir = previous }
}
