package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"specific-harness/internal/config"
)

type commandSpec struct {
	summary, positional, flags string
	min, max                   int
}

var commandOrder = strings.Fields("version config chat hw tool run graph sessions judgments template launch retire list decision tui")
var commandSpecs = map[string]commandSpec{
	"version":   {"print version", "", "", 0, 0},
	"config":    {"print effective YAML config", "", "config", 0, 0},
	"chat":      {"one model prompt (no agent tools)", "", "config server model prompt no-stream", 0, 0},
	"hw":        {"print hardware profile and limits", "", "", 0, 0},
	"tool":      {"list standalone primitive schemas / show one schema", "[name]", "config", 0, 1},
	"run":       {"headless coding task; completion requires an actual declared check", "", "config server model prompt tools max-steps", 0, 0},
	"graph":     {"run one in-process GraphQL operation", "", "config db sessions-dir query", 0, 0},
	"sessions":  {"list past runs / show one run", "[id]", "config sessions-dir", 0, 1},
	"judgments": {"summarize one run's judgments", "<id>", "config sessions-dir", 1, 1},
	"template":  {"render an existing scaffold", "<name>", "config templates-dir title", 1, 1},
	"launch":    {"fan out one approach", "<approach-id>", "config server model max-steps", 1, 1},
	"retire":    {"retire one complete plan or approach", "<id>", "config", 1, 1},
	"list":      {"list plans or one plan's approaches", "plans | approaches <plan-id>", "config retired", 1, 2},
	"decision":  {"judge comma-separated options", "", "config context instructions options server device", 0, 0},
	"tui":       {"interactive coding terminal", "", "config resume", 0, 0},
}

type cliArgs struct {
	explicitConfig bool
	values         map[string]string
	positionals    []string
	help           bool
}

func (a cliArgs) loadConfig() (config.Config, bool, error) {
	if a.explicitConfig {
		return config.LoadProject(a.string("config"))
	}
	return config.Load(a.string("config"))
}

func (a cliArgs) string(name string) string { return a.values[name] }
func (a cliArgs) bool(name string) bool     { return a.values[name] == "true" }
func (a cliArgs) integer(name string) int   { n, _ := strconv.Atoi(a.values[name]); return n }

func commandFlags(command string) *flag.FlagSet {
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Bool("help", false, "show help without loading config or running anything")
	for _, name := range strings.Fields(commandSpecs[command].flags) {
		switch name {
		case "no-stream", "retired":
			fs.Bool(name, false, "enable this option")
		case "max-steps":
			fs.String(name, "", "tool-call limit; 0 means no call ceiling; omitted uses config")
		case "config":
			fs.String(name, config.DefaultPath, "YAML path; existing project file takes precedence, otherwise workspace settings in user data")
		default:
			fs.String(name, "", "value; otherwise use config where applicable")
		}
	}
	return fs
}

// Collect options independently of positionals so documented flags after IDs work.
// A value resembling an option uses --flag=value, avoiding missing-value ambiguity.
func parseCLI(argv []string) (string, cliArgs, error) {
	a := cliArgs{values: map[string]string{}}
	if len(argv) == 0 {
		return "", a, fmt.Errorf("no command")
	}
	command := argv[0]
	if command == "help" || command == "--help" || command == "-h" {
		if len(argv) == 1 {
			a.help = true
			return "", a, nil
		}
		if command != "help" || len(argv) != 2 {
			return "", a, fmt.Errorf("unexpected help arguments")
		}
		argv = []string{argv[1], "--help"}
		command = argv[0]
	}
	spec, ok := commandSpecs[command]
	if !ok {
		return command, a, fmt.Errorf("unknown command %q", command)
	}
	fs := commandFlags(command)
	var options []string
	for i := 1; i < len(argv); i++ {
		s := argv[i]
		if s == "--" {
			a.positionals = append(a.positionals, argv[i+1:]...)
			break
		}
		if s == "-h" {
			s = "--help"
		}
		if !strings.HasPrefix(s, "-") || s == "-" {
			a.positionals = append(a.positionals, s)
			continue
		}
		name, value, equals := strings.Cut(strings.TrimPrefix(s, "--"), "=")
		f := fs.Lookup(name)
		if !strings.HasPrefix(s, "--") || f == nil {
			return command, a, fmt.Errorf("unknown flag %q for %s", s, command)
		}
		options = append(options, s)
		boolean := isBoolFlag(f)
		if !boolean && !equals {
			if i+1 == len(argv) || strings.HasPrefix(argv[i+1], "--") || argv[i+1] == "-h" {
				return command, a, fmt.Errorf("missing value for --%s", name)
			}
			i++
			value = argv[i]
			options = append(options, value)
		}
		if name == "max-steps" {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return command, a, fmt.Errorf("invalid --max-steps: expected a nonnegative integer (0 means no call ceiling)")
			}
		}
	}
	if err := fs.Parse(options); err != nil {
		return command, a, fmt.Errorf("%s: %w", command, err)
	}
	fs.VisitAll(func(f *flag.Flag) { a.values[f.Name] = f.Value.String() })
	// Resolve legacy project config only for execution, after help/syntax have
	// been admitted. Keep help's advertised default independent of the cwd.
	if fs.Lookup("config") != nil && !a.bool("help") {
		explicit := false
		fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
		a.explicitConfig = explicit
		if !explicit {
			a.values["config"] = config.DefaultFile()
		}
	}
	if view := a.string("tools"); view != "" && view != "coding" && view != "full" {
		return command, a, fmt.Errorf("--tools must be coding or full")
	}
	a.help = a.bool("help")
	if len(a.positionals) > spec.max || !a.help && len(a.positionals) < spec.min {
		return command, a, fmt.Errorf("invalid %s arguments; expected %s", command, spec.positional)
	}
	if a.help && len(a.positionals) == 0 {
		return command, a, nil
	}
	if command == "list" && !(len(a.positionals) == 1 && a.positionals[0] == "plans" || len(a.positionals) == 2 && a.positionals[0] == "approaches") {
		return command, a, fmt.Errorf("invalid list arguments; expected plans or approaches <plan-id>")
	}
	return command, a, nil
}

func printCLIHelp(out io.Writer, command string) {
	if command == "" {
		fmt.Fprintln(out, "Caliper Harness · Specific Labs")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "usage: caliper-harness [command] [options]\n       caliper-harness help <command>")
		for _, name := range commandOrder {
			spec := commandSpecs[name]
			fmt.Fprintf(out, "  %-10s %s %s\n", name, spec.summary, spec.positional)
		}
		fmt.Fprintln(out, "\nRun caliper-harness with no arguments to open the terminal interface.\nFirst use: Setup stages an existing model and your exact project check, then asks you to save.\nUse /setup to return, /settings for personal or project preferences, /status for runtime metrics, and /manage for models and servers.\nExisting commands remain available:\n  caliper-harness tui --config caliper-harness.yaml\n  caliper-harness run --config caliper-harness.yaml --prompt 'Inspect this project and run its check'\nCoding is the default agent view; --tools full exposes existing planning/memory tools.\n`tool` inspects standalone primitive schemas, not the agent tool view.")
		return
	}
	spec := commandSpecs[command]
	fmt.Fprintf(out, "usage: caliper-harness %s %s [options]\n%s\n", command, spec.positional, spec.summary)
	fs := commandFlags(command)
	fs.VisitAll(func(f *flag.Flag) {
		value := " VALUE"
		if isBoolFlag(f) {
			value = ""
		}
		fmt.Fprintf(out, "  --%s%s  %s", f.Name, value, f.Usage)
		if f.DefValue != "" && f.DefValue != "false" {
			fmt.Fprintf(out, " (default %s)", f.DefValue)
		}
		fmt.Fprintln(out)
	})
	fmt.Fprintln(out, "Flags may precede or follow positionals; -- ends options. Use --flag=value for values beginning with --.\nUnspecified model/runtime/limits use config; help performs no configuration or runtime work.")
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}
