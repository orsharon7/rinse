// sessions_test_hook.go — exposes an internal sessions directory override
// for test isolation without mutating process-wide environment variables.
package stats

// SetSessionsDir overrides the directory returned by SessionsDir.
// Call from tests to redirect session I/O to a temp directory.
// Returns a restore function that resets the override to its previous value.
func SetSessionsDir(dir string) func() {
	old := sessionsDirOverride
	sessionsDirOverride = dir
	return func() { sessionsDirOverride = old }
}
