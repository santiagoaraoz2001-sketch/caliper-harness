//go:build darwin

package serve

import "specific-harness/internal/bundle"

// driverRecipes is the owned MLX worker: the one server Caliper itself
// launches with a private runtime on this platform. Everything else is
// discovered, relaunched, or user-registered — never hardcoded.
func driverRecipes(harnessDir, workerModel string) []Recipe {
	mlx := Recipe{Name: "mlx", Kind: KindOpenAI, Runtime: "mlx", URL: MLXURL, Argv: []string{"mlx_lm.server", "--model", workerModel, "--port", "8080"}}
	if bundle.Managed(harnessDir) {
		mlx.Argv = []string{bundle.Python(harnessDir, "worker"), "-m", "mlx_lm.server", "--model", workerModel, "--port", "8080"}
		mlx.managedPython = true
	}
	return []Recipe{mlx}
}

// checkpointRuntime labels discovered local safetensors checkpoints.
func checkpointRuntime() string { return "mlx" }
