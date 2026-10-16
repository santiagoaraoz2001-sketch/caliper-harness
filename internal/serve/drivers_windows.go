//go:build windows

package serve

// driverRecipes exposes the Rulith manager handle when Rulith Inference
// is installed. The recipe carries no Argv: starting goes through the
// manager (JSON stdio), never a generic process spawn, because only the
// manager knows its gates, profiles, and placement. MLX is Apple-only
// and deliberately absent from this build.
func driverRecipes(harnessDir, workerModel string) []Recipe {
	install, ok := locateRulith()
	if !ok {
		return nil
	}
	return []Recipe{{Name: "rulith", Kind: KindRulith, Runtime: "rulith", URL: install.EndpointHint()}}
}
