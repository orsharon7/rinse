// state_test_hook.go — only compiled for tests.
// Exposes internal state directory for test isolation.
package runner

// SetStateDir overrides the stateDir used by loadState/saveState/clearState.
// Call from tests to redirect state I/O to a temp directory.
func SetStateDir(dir string) {
	stateDir = dir
}
