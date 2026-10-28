package config

// WithRuntime changes a selection without carrying a different runtime's
// inherited protocol. Callers setting an explicit protocol still validate it.
func (d DecisionConfig) WithRuntime(runtime string) DecisionConfig {
	if d.Runtime != runtime {
		d.Protocol = ""
	}
	d.Runtime = runtime
	return d
}
