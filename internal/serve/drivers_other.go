//go:build !darwin && !windows

package serve

// driverRecipes: no owned private runtime on this platform. Servers
// are discovered read-only or launched through explicit user recipes.
func driverRecipes(harnessDir, workerModel string) []Recipe { return nil }
