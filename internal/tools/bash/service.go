package bash

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"specific-harness/internal/config"
	"specific-harness/internal/prompts"
	"specific-harness/internal/serve"
	"specific-harness/internal/tool"
)

// ServiceOwner is a session lifetime, distinct from an ordinary command call.
// It reuses serve's process-tree ownership, owns at most one foreground service,
// and never registers startup jobs or searches for unrelated processes.
type ServiceOwner struct {
	mu            sync.Mutex
	proc          *serve.Proc
	log, dir      string
	logIdentity   os.FileInfo
	closed        bool
	scriptCleanup func()
}

func (s *ServiceOwner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.proc != nil {
		if err := s.proc.Stop(); err != nil {
			return err
		}
	}
	s.clearScript()
	return nil
}

func (s *ServiceOwner) clearScript() {
	if s.scriptCleanup != nil {
		s.scriptCleanup()
		s.scriptCleanup = nil
	}
}

// NewAtOwned exposes the additional lifetime only when the caller supplies and
// closes a session owner. Headless/standalone definitions remain ordinary bash.
func NewAtOwned(cfg config.BashConfig, dir string, owner *ServiceOwner, catalogs ...*prompts.Catalog) tool.Definition {
	d := NewAt(cfg, dir, catalogs...)
	if owner == nil {
		return d
	}
	pc := prompts.Select(catalogs...)
	d.Params[0].Required = false              // status/stop have no shell command
	d.Params[1].Default = ""                  // ordinary run supplies its default; services have no call deadline
	d.Example = tool.Args{"cmd": "<command>"} // optional conditional cmd still needs a usable ordinary example
	d.Params = append(d.Params, tool.Param{Name: "service", Type: tool.TypeString, Enum: []string{"start", "status", "stop"}, ExactEnum: true, Desc: pc.Text("tools.bash.parameters.service")})
	d.Purpose += "\n" + pc.Text("guidance.bash.service")
	ordinary := d.Execute
	d.Execute = func(ctx context.Context, args tool.Args) (tool.Result, error) {
		action, _ := args["service"].(string)
		command, _ := args["cmd"].(string)
		if action == "" {
			if strings.TrimSpace(command) == "" {
				return tool.Result{}, fmt.Errorf("cmd is required for an ordinary command")
			}
			return ordinary(ctx, args)
		}
		if timeout, ok := args["timeout"]; ok && timeout != nil {
			return tool.Result{}, fmt.Errorf("timeout applies only to ordinary commands; omit it for service=%s (session lifetime)", action)
		}
		return owner.execute(ctx, action, command, dir)
	}
	return d
}

func (s *ServiceOwner) execute(ctx context.Context, action, command, dir string) (tool.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return tool.Result{}, err
	}
	if s.closed {
		return tool.Result{}, fmt.Errorf("service session is closed")
	}
	if action != "start" && command != "" {
		return tool.Result{}, fmt.Errorf("service %s does not execute cmd; omit cmd", action)
	}
	switch action {
	case "stop":
		if s.proc != nil {
			if err := s.proc.Stop(); err != nil {
				return tool.Result{}, err
			}
			s.proc = nil
		}
		s.clearScript()
		return tool.Result{Text: "Owned session service stopped."}, nil
	case "status":
		return tool.Result{Text: s.status() + s.diagnostic()}, nil
	case "start":
		if strings.TrimSpace(command) == "" {
			return tool.Result{}, fmt.Errorf("service=start requires a foreground cmd")
		}
		if s.proc != nil && s.proc.PID() != 0 {
			return tool.Result{}, fmt.Errorf("a session service is already running; stop it before replacement. %s", s.status())
		}
		shell, err := ShellPath()
		if err != nil {
			return tool.Result{}, err
		}
		log, err := os.CreateTemp("", "caliper-service-*.log")
		if err != nil {
			return tool.Result{}, err
		}
		s.log = log.Name()
		s.logIdentity, err = log.Stat()
		_ = log.Close()
		if err != nil {
			return tool.Result{}, err
		}
		s.dir, err = filepath.Abs(dir)
		if err != nil {
			return tool.Result{}, err
		}
		s.clearScript()
		argsv, cleanup, err := scriptArgs(command)
		if err != nil {
			return tool.Result{}, err
		}
		s.scriptCleanup = cleanup
		s.proc, err = serve.StartContext(ctx, serve.Recipe{Name: "session service", Argv: append([]string{shell}, argsv...), Dir: s.dir, LogMaxBytes: 4 << 20}, s.log)
		if err != nil {
			s.clearScript()
			return tool.Result{}, err
		}
		if err = ctx.Err(); err != nil {
			_ = s.proc.Stop()
			s.proc = nil
			s.clearScript()
			return tool.Result{}, err
		}
		return tool.Result{Text: s.status() + "\nCheck the intended endpoint before claiming readiness. Use service=stop to stop only this process tree."}, nil
	default:
		return tool.Result{}, fmt.Errorf("unknown service action %q", action)
	}
}

// Read only the owner-created file, and only on explicit status requests.
// This gives the model diagnostics without granting arbitrary TEMP-file reads.
func (s *ServiceOwner) diagnostic() string {
	if s.log == "" || s.logIdentity == nil {
		return ""
	}
	const capBytes int64 = 2048
	const retainedBytes int64 = 4 << 20
	info, err := os.Lstat(s.log)
	if err != nil || !info.Mode().IsRegular() {
		return "\nOwned diagnostic unavailable: log missing or replaced."
	}
	f, err := os.Open(s.log)
	if err != nil {
		return "\nOwned diagnostic unavailable: cannot open retained log."
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !os.SameFile(s.logIdentity, info) {
		return "\nOwned diagnostic unavailable: log identity changed."
	}
	end := min(info.Size(), retainedBytes)
	start := max(int64(0), end-capBytes)
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return "\nOwned diagnostic unavailable: cannot seek retained log."
	}
	raw, err := io.ReadAll(io.LimitReader(f, end-start))
	if err != nil {
		return "\nOwned diagnostic unavailable: cannot read retained log."
	}
	if len(raw) == 0 {
		return "\nRetained service log is empty."
	}
	return "\nRetained service log excerpt (last up to 2 KiB of the first 4 MiB; may be truncated):\n" + strings.ToValidUTF8(string(raw), "?")
}

func (s *ServiceOwner) status() string {
	pid := 0
	if s.proc != nil {
		pid = s.proc.PID()
	}
	if pid == 0 {
		return "No running owned session service. Log: " + s.log
	}
	return fmt.Sprintf("Owned session service running: PID %d; cwd %s; log %s (first 4 MiB). Endpoint health is unverified.", pid, s.dir, s.log)
}
