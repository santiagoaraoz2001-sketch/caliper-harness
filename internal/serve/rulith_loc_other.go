//go:build !windows

package serve

// LocateRulith reports no install off Windows: Rulith Inference is a
// Windows product (AMD Strix Halo). Its endpoint can still be added as
// a generic OpenAI server when reached over the network.
func LocateRulith() (RulithInstall, bool) { return RulithInstall{}, false }
