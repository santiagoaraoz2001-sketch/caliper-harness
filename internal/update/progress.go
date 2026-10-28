package update

import (
	"context"
	"io"
	"time"
)

// Progress is an observation of the current phase, never an overall percentage
// or ETA. TotalBytes is zero when the server/manifest does not provide it.
type Progress struct {
	Phase             string
	Bytes, TotalBytes int64
	Files, TotalFiles int
	Elapsed           time.Duration
}

type progressReporter struct {
	callback    func(Progress)
	start, last time.Time
	value       Progress
}

func (m *Manager) beginProgress() {
	m.progress = &progressReporter{callback: m.Progress, start: time.Now()}
}

func (m Manager) phase(ctx context.Context, phase string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.progress != nil {
		m.progress.value.Phase = phase
		m.progress.emit(true)
	}
	return ctx.Err()
}

func (p *progressReporter) emit(force bool) {
	if p == nil || p.callback == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(p.last) < 250*time.Millisecond {
		return
	}
	p.last = now
	p.value.Elapsed = now.Sub(p.start)
	p.callback(p.value)
}

type progressWriter struct {
	ctx      context.Context
	dst      io.Writer
	progress *progressReporter
}

func (w progressWriter) Write(b []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.dst.Write(b)
	if w.progress != nil {
		w.progress.value.Bytes += int64(n)
		w.progress.emit(false)
	}
	if err == nil {
		err = w.ctx.Err()
	}
	return n, err
}

type contextReader struct {
	ctx context.Context
	src io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.src.Read(b)
}

func (m Manager) verify(ctx context.Context, root string) error {
	if err := m.phase(ctx, "Verifying installed files"); err != nil {
		return err
	}
	return validateInventoryContext(ctx, root, func(done, total int) {
		if m.progress != nil {
			m.progress.value.Files, m.progress.value.TotalFiles = done, total
			m.progress.emit(done == total)
		}
	})
}
