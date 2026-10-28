package session

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Reserve atomically owns a fresh archive ID with its first event. Unlike
// Append it never adopts an existing run, including an interrupted reservation.
func Reserve(dir, id string, e Event) error {
	path, err := resolve(dir, id)
	if err != nil {
		return err
	}
	if e.Kind == "" {
		return fmt.Errorf("session event kind cannot be empty")
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("cannot reserve fresh session %s: %w", id, err)
	}
	if err = json.NewEncoder(f).Encode(e); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
