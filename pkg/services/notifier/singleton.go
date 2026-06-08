package notifier

import (
	"fmt"
	"sync"
)

var (
	globalMu sync.RWMutex
	global   *Notifier
)

// SetGlobal installs n as the process-wide notifier and stops any previous one.
// Passing nil disables global Emit (it becomes a no-op).
func SetGlobal(n *Notifier) {
	globalMu.Lock()
	old := global
	global = n
	globalMu.Unlock()
	if old != nil && old != n {
		old.Stop()
	}
}

// Global returns the installed notifier, or nil.
func Global() *Notifier {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Emit sends an event through the global notifier. No-op when none is set, so
// event sites can call it unconditionally.
func Emit(ev Event) {
	globalMu.RLock()
	n := global
	globalMu.RUnlock()
	if n != nil {
		n.Emit(ev)
	}
}

func errUnknownChannel(name string) error {
	return fmt.Errorf("notifier: no channel named %q", name)
}
