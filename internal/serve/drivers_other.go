//go:build !darwin && !windows

package serve

// driverRecipes: no owned private runtime on this platform. Servers
// are discovered, relaunched, or user-registered — never hardcoded.
func driverRecipes(harnessDir, workerModel string) []Recipe { return nil }

// checkpointRuntime labels discovered local safetensors checkpoints.
func checkpointRuntime() string { return "checkpoint" }
