// Package watch tails one working tree: debounced per-file events
// with trimmed before/after diffs for the diff view.
package watch

import (
	"bytes"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Change kinds.
const (
	OpCreate = "create"
	OpModify = "modify"
	OpDelete = "delete"
)

// Event is one coalesced file change.
type Event struct {
	Seq            int
	Path           string // slash-separated, relative to the root
	Op             string
	At             time.Time
	Added          []string
	Removed        []string
	AddedN         int // totals before capping
	RemovedN       int
	After          []string // file content after the change, capped
	Truncated      bool
	AfterTool      string // stamped by the TUI, not the watcher
	BeforeKnown    bool
	BeforePartial  bool
	ReadError      string
	OutsideCapture bool // changed bytes were observed outside the retained line prefix
}

// Config tunables.
type Config struct {
	Debounce          time.Duration
	MaxBytes          int
	StartupMaxBytes   int
	StartupMaxFiles   int
	StartupMaxEntries int
	DiffLines         int
	FileLines         int
	Ignore            []string
}

// DefaultConfig is the watcher baseline: fast, small, quiet on
// harness-owned state.
func DefaultConfig() Config {
	return Config{
		Debounce:          200 * time.Millisecond,
		MaxBytes:          1 << 17,
		StartupMaxBytes:   8 << 20,
		StartupMaxFiles:   512,
		StartupMaxEntries: 4096,
		DiffLines:         6,
		FileLines:         200,
		Ignore:            []string{".git", "data", "node_modules"},
	}
}

type snap struct {
	hash    uint64
	lines   []string
	partial bool
}

// Stats describes observed startup coverage and delivery loss, not a full-tree guarantee.
type Stats struct {
	Attempts, Files, Bytes, Partial, Omitted, Skipped, Dropped, Errors int
	Error                                                              string
	Initializing                                                       bool
	Entries                                                            int
	Limited                                                            bool
}

type pending struct {
	op    string
	timer *time.Timer
}

// Watcher tails root recursively. Events flow to Events; drain it
// until Close. Sends never block: a full buffer drops and counts.
type Watcher struct {
	root  string
	cfg   Config
	fw    *fsnotify.Watcher
	out   chan Event
	done  chan struct{}
	ready chan struct{}

	mu      sync.Mutex
	closed  bool
	seq     int
	snaps   map[string]snap
	pend    map[string]*pending
	dropped int
	skipped int
	stats   Stats
}

// New discovers watches within the configured entry budget. Root failures and exhausted
// descriptors return errors; ordinary unreadable subtrees count in Skipped.
func New(root string, cfg Config) (*Watcher, error) {
	return newWatcher(root, cfg, false, filepath.WalkDir)
}

// NewAsync registers the root immediately, then discovers subdirectories and
// captures the bounded baseline in the background. A large workspace must not
// prevent the terminal interface from opening. Stats reports partial coverage
// while initialization is running; Ready closes when it settles or fails.
func NewAsync(root string, cfg Config) (*Watcher, error) {
	return newWatcher(root, cfg, true, filepath.WalkDir)
}

type walkTree func(string, fs.WalkDirFunc) error

func newWatcher(root string, cfg Config, background bool, walk walkTree) (*Watcher, error) {
	if cfg.MaxBytes <= 0 || cfg.StartupMaxBytes <= 0 || cfg.StartupMaxFiles <= 0 || cfg.StartupMaxEntries <= 0 || cfg.FileLines <= 0 || cfg.DiffLines <= 0 {
		return nil, fmt.Errorf("watch max_file_bytes/startup_max_bytes/startup_max_files/startup_max_entries and line caps must be positive")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{
		root:  abs,
		cfg:   cfg,
		fw:    fw,
		out:   make(chan Event, 256),
		done:  make(chan struct{}),
		ready: make(chan struct{}),
		snaps: map[string]snap{},
		pend:  map[string]*pending{},
	}
	if err := fw.Add(abs); err != nil {
		return nil, errors.Join(err, closeWatch(fw, abs))
	}
	w.stats.Initializing = true
	if background {
		go func() {
			if err := w.initialize(walk); err != nil {
				w.mu.Lock()
				w.stats.Error = err.Error()
				w.stats.Errors++
				w.mu.Unlock()
				_ = w.Close()
				return
			}
			w.pump()
		}()
		return w, nil
	}
	if err := w.initialize(walk); err != nil {
		return nil, errors.Join(err, w.Close())
	}
	go w.pump()
	return w, nil
}

func (w *Watcher) initialize(walk walkTree) error {
	// The scanner owns these until it publishes them. No filesystem operation
	// holds mu: Stats and Close must remain responsive even on a slow/cloud path.
	stats := Stats{Initializing: true}
	snaps := map[string]snap{}
	skipped := 0
	defer func() {
		w.mu.Lock()
		stats.Initializing = false
		w.stats, w.skipped, w.snaps = stats, skipped, snaps
		w.mu.Unlock()
		close(w.ready)
	}()
	err := walk(w.root, func(p string, d os.DirEntry, err error) error {
		select {
		case <-w.done:
			return filepath.SkipAll
		default:
		}
		defer func() {
			w.mu.Lock()
			w.stats, w.skipped = stats, skipped
			w.mu.Unlock()
		}()
		if err != nil {
			if descriptorExhausted(err) {
				return err
			}
			skipped++
			stats.Error = err.Error()
			return filepath.SkipDir
		}
		if p == w.root {
			return nil
		}
		if stats.Entries >= w.cfg.StartupMaxEntries {
			stats.Limited = true
			return filepath.SkipAll
		}
		stats.Entries++
		rel, _ := filepath.Rel(w.root, p)
		rel = filepath.ToSlash(rel)
		if w.ignored(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			if !d.Type().IsRegular() {
				return nil
			}
			if stats.Attempts >= w.cfg.StartupMaxFiles || stats.Bytes >= w.cfg.StartupMaxBytes {
				stats.Omitted++
				return nil
			}
			stats.Attempts++
			ss, n, err := w.read(p, min(w.cfg.MaxBytes, w.cfg.StartupMaxBytes-stats.Bytes))
			stats.Bytes += n
			if err != nil {
				if descriptorExhausted(err) {
					return err
				}
				stats.Omitted++
				stats.Error = err.Error()
				return nil
			}
			snaps[rel] = ss
			stats.Files++
			if ss.partial {
				stats.Partial++
			}
			return nil
		}
		if err := w.fw.Add(p); err != nil {
			// Release any partially registered kqueue watch before returning.
			_ = w.fw.Remove(p)
			if descriptorExhausted(err) {
				return err
			}
			skipped++
			stats.Error = err.Error()
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("watch startup: %w", err)
	}
	return nil
}

func (w *Watcher) Ready() <-chan struct{} { return w.ready }

// fsnotify v1.9.0 kqueue Close marks itself closed before Remove, so its
// removal becomes a no-op. Remove our watches first, including a root whose
// Add may have partially succeeded before exhausting descriptors.
func closeWatch(fw *fsnotify.Watcher, attempted ...string) error {
	var errs []error
	for _, path := range append(attempted, fw.WatchList()...) {
		if err := fw.Remove(path); err != nil && !errors.Is(err, fsnotify.ErrNonExistentWatch) {
			errs = append(errs, err)
		}
	}
	return errors.Join(append(errs, fw.Close())...)
}

func descriptorExhausted(err error) bool {
	return errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE)
}

// Root is the watched tree.
func (w *Watcher) Root() string { return w.root }

// Events receives coalesced changes.
func (w *Watcher) Events() <-chan Event { return w.out }

// Dropped counts events lost to a full buffer; Skipped counts
// subtrees New could not watch.
func (w *Watcher) Dropped() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dropped
}
func (w *Watcher) Skipped() int { return w.Stats().Skipped }
func (w *Watcher) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.stats
	s.Skipped, s.Dropped = w.skipped, w.dropped
	return s
}

// Close stops the watcher. Sends stop; the channel is abandoned.
func (w *Watcher) Close() error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return nil
	}
	w.closed = true
	for _, p := range w.pend {
		p.timer.Stop()
	}
	w.mu.Unlock()
	close(w.done)
	return closeWatch(w.fw, w.root)
}

func (w *Watcher) pump() {
	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handle(ev)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			w.mu.Lock()
			w.stats.Error = err.Error()
			w.stats.Errors++
			w.snaps = map[string]snap{} // Lost kernel coverage; later reads establish new baselines.
			w.mu.Unlock()
		}
	}
}

func (w *Watcher) rel(path string) (string, bool) {
	rel, err := filepath.Rel(w.root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (w *Watcher) ignored(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	for _, ig := range w.cfg.Ignore {
		ig = strings.Trim(strings.ReplaceAll(ig, "\\", "/"), "/")
		if ig == "" {
			continue
		}
		if rel == ig || strings.HasPrefix(rel, ig+"/") {
			return true
		}
		// Package managers also create dependency trees inside workspace packages.
		// Keep the default exclusion at any depth without broadening path ignores.
		if ig == "node_modules" && strings.Contains("/"+rel+"/", "/node_modules/") {
			return true
		}
	}
	return false
}

// addTree watches a new directory and reconciles anything already
// under it: creations that beat the pump have no queued event, so
// files without a baseline emit one synthetic create. A real event
// for the same file still drops on the hash check — exactly one
// event either way.
func (w *Watcher) addTree(path string) {
	rel, ok := w.rel(path)
	if !ok || w.ignored(rel) {
		return
	}
	entries := 0
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		select {
		case <-w.done:
			return filepath.SkipAll
		default:
		}
		if entries >= w.cfg.StartupMaxEntries {
			w.mu.Lock()
			w.stats.Limited = true
			w.mu.Unlock()
			return filepath.SkipAll
		}
		entries++
		if err != nil {
			return nil
		}
		prel, ok := w.rel(p)
		if !ok || w.ignored(prel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			w.mu.Lock()
			if w.closed {
				w.mu.Unlock()
				return filepath.SkipAll
			}
			err := w.fw.Add(p)
			if err != nil {
				w.skipped++
				w.stats.Error = err.Error()
			}
			w.mu.Unlock()
			if err != nil {
				return filepath.SkipDir
			}
			return nil
		}
		w.mu.Lock()
		_, known := w.snaps[prel]
		w.mu.Unlock()
		if known {
			return nil
		}
		ss, _, err := w.read(p, w.cfg.MaxBytes)
		if err != nil {
			w.mu.Lock()
			w.stats.Error = err.Error()
			w.mu.Unlock()
			return nil
		}
		ev := Event{Op: OpCreate, Path: prel, At: time.Now(), After: ss.lines, Truncated: ss.partial}
		w.mu.Lock()
		ev.BeforeKnown = !w.stats.Limited && w.stats.Omitted == 0 && w.skipped == 0 && w.stats.Error == ""
		if ev.BeforeKnown {
			ev.Added, ev.AddedN = capLines(ss.lines, w.cfg.DiffLines)
		}
		w.snaps[prel] = ss
		w.mu.Unlock()
		w.emit(ev, false, prel)
		return nil
	})
}

func (w *Watcher) handle(ev fsnotify.Event) {
	rel, ok := w.rel(ev.Name)
	if !ok || rel == "." || w.ignored(rel) {
		return
	}
	if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
		if ev.Op&(fsnotify.Create|fsnotify.Rename) != 0 {
			w.addTree(ev.Name)
		}
		return
	}
	var op string
	switch {
	case ev.Op&fsnotify.Remove != 0:
		op = OpDelete
	case ev.Op&fsnotify.Rename != 0:
		op = OpDelete
	case ev.Op&fsnotify.Create != 0:
		op = OpCreate
	default:
		op = OpModify
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	p, dup := w.pend[rel]
	if !dup {
		p = &pending{}
		w.pend[rel] = p
	} else {
		p.timer.Stop()
	}
	p.op = mergeOp(p.op, op, !dup)
	if p.op == "" {
		delete(w.pend, rel)
		return
	}
	p.timer = time.AfterFunc(w.cfg.Debounce, func() { w.fire(rel) })
}

// mergeOp folds a new raw op into the pending one. Net-zero
// create+delete cancels to "".
func mergeOp(cur, next string, first bool) string {
	if first {
		return next
	}
	switch cur + "+" + next {
	case OpCreate + "+" + OpModify:
		return OpCreate
	case OpCreate + "+" + OpDelete:
		return ""
	case OpModify + "+" + OpDelete:
		return OpDelete
	case OpDelete + "+" + OpCreate:
		return OpModify
	default:
		return next
	}
}

func (w *Watcher) fire(rel string) {
	w.mu.Lock()
	p, ok := w.pend[rel]
	if !ok || w.closed {
		w.mu.Unlock()
		return
	}
	delete(w.pend, rel)
	op := p.op
	old, haveOld := w.snaps[rel]
	w.mu.Unlock()

	st := w.Stats()
	ev := Event{Op: op, Path: rel, At: time.Now(), BeforeKnown: haveOld, BeforePartial: old.partial}
	ss, _, err := w.read(filepath.Join(w.root, filepath.FromSlash(rel)), w.cfg.MaxBytes)
	if err != nil {
		if os.IsNotExist(err) {
			ev.Op = OpDelete
			if haveOld {
				ev.Removed, ev.RemovedN = capLines(old.lines, w.cfg.DiffLines)
				ev.After, ev.Truncated = old.lines, old.partial
			}
		} else {
			ev.Op = OpModify
			ev.ReadError = err.Error()
			ev.After, ev.Truncated = old.lines, old.partial
			w.mu.Lock()
			w.stats.Error = err.Error()
			w.mu.Unlock()
		}
		w.emit(ev, os.IsNotExist(err), rel)
		return
	}
	ev.Truncated, ev.After = ss.partial, ss.lines
	if ev.BeforeKnown && !old.partial && !ss.partial && ss.hash == old.hash {
		return
	}
	if ev.BeforeKnown {
		ev.Added, ev.Removed, ev.AddedN, ev.RemovedN = diffLines(old.lines, ss.lines, w.cfg.DiffLines)
		ev.OutsideCapture = (old.partial || ss.partial) && old.hash != ss.hash && ev.AddedN == 0 && ev.RemovedN == 0
	} else {
		ev.BeforeKnown = op == OpCreate && !st.Limited && st.Omitted == 0 && st.Skipped == 0 && st.Error == ""
		if ev.BeforeKnown {
			ev.Added, ev.AddedN = capLines(ss.lines, w.cfg.DiffLines)
		}
	}
	w.mu.Lock()
	w.snaps[rel] = ss
	w.mu.Unlock()
	w.emit(ev, true, rel)
}

func (w *Watcher) emit(ev Event, store bool, rel string) {
	if ev.Op == OpDelete && store {
		w.mu.Lock()
		delete(w.snaps, rel)
		w.mu.Unlock()
	}
	w.mu.Lock()
	ev.Seq = w.seq
	w.seq++
	select {
	case w.out <- ev:
	default:
		w.dropped++
	}
	w.mu.Unlock()
}

// read bounds allocation before decoding; the separate overflow probe is never retained.
func (w *Watcher) read(path string, limit int) (snap, int, error) {
	entry, err := os.Lstat(path)
	if err != nil {
		return snap{}, 0, err
	}
	if !entry.Mode().IsRegular() {
		return snap{}, 0, fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return snap{}, 0, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return snap{}, 0, err
	}
	if !before.Mode().IsRegular() || !os.SameFile(entry, before) {
		return snap{}, 0, fmt.Errorf("not a regular file: %s", path)
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return snap{}, len(raw), err
	}
	var probe [1]byte
	n, err := f.Read(probe[:])
	if err != nil && err != io.EOF {
		return snap{}, len(raw), err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return snap{}, len(raw), err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return snap{}, len(raw), fmt.Errorf("changed during capture: %s", path)
	}
	lines, partial := readLines(raw, limit, w.cfg.FileLines)
	return snap{hash: hash(raw), lines: lines, partial: partial || n > 0}, len(raw), nil
}

func hash(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

// readLines splits content into lines, capping bytes and rows.
// Binary content collapses to one marker row.
func readLines(b []byte, maxBytes, maxLines int) ([]string, bool) {
	truncated := len(b) > maxBytes
	if truncated {
		b = b[:maxBytes]
	}
	if len(b) == 0 {
		return nil, false
	}
	if bytes.IndexByte(b, 0) >= 0 {
		return []string{"<binary file>"}, true
	}
	lines := strings.Split(string(b), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(lines) > maxLines {
		return lines[:maxLines], true
	}
	return lines, truncated
}

func capLines(lines []string, max int) ([]string, int) {
	if len(lines) <= max {
		return lines, len(lines)
	}
	return lines[:max], len(lines)
}

// diffLines trims the common prefix/suffix, capping each side.
func diffLines(old, new []string, max int) (added, removed []string, an, rn int) {
	p := 0
	for p < len(old) && p < len(new) && old[p] == new[p] {
		p++
	}
	s := 0
	for s < len(old)-p && s < len(new)-p && old[len(old)-1-s] == new[len(new)-1-s] {
		s++
	}
	removed, rn = capLines(old[p:len(old)-s], max)
	added, an = capLines(new[p:len(new)-s], max)
	return added, removed, an, rn
}
