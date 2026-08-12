// Package routing executes a policy decision — it makes a chosen bearer the
// active egress path (spec §10, §3.3). It is deliberately pluggable: the DryRun
// manager (the safe default) records the intended change, while the Linux
// manager applies it to the kernel routing table. The policy engine never
// touches the OS directly.
package routing

import (
	"fmt"
	"os/exec"
	"sync"
)

// Manager makes a bearer the active egress path.
type Manager interface {
	Activate(iface string) error
	Active() string
}

// DryRun records the intended active interface without changing the system.
// It is the default so the agent is safe to run anywhere — CI, a laptop, or a
// node where you don't yet want it steering real traffic.
type DryRun struct {
	mu     sync.Mutex
	active string
}

// NewDryRun returns a DryRun manager.
func NewDryRun() *DryRun { return &DryRun{} }

// Activate records iface as the active path.
func (d *DryRun) Activate(iface string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.active = iface
	return nil
}

// Active returns the recorded active interface.
func (d *DryRun) Active() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

// Linux applies the active path by replacing the default route (spec §23).
// It requires CAP_NET_ADMIN (root).
type Linux struct {
	mu     sync.Mutex
	active string
	run    func(args ...string) error // overridable for testing
}

// NewLinux returns a Linux route manager that shells out to `ip`.
func NewLinux() *Linux { return &Linux{run: runIP} }

// Activate replaces the default route so it egresses iface.
func (l *Linux) Activate(iface string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.run(DefaultRouteArgs(iface)...); err != nil {
		return fmt.Errorf("activate %s: %w", iface, err)
	}
	l.active = iface
	return nil
}

// Active returns the interface last activated successfully.
func (l *Linux) Active() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// DefaultRouteArgs builds the `ip` arguments that make iface the default route.
// Exposed and unit-tested so the command is verifiable without executing it.
func DefaultRouteArgs(iface string) []string {
	return []string{"route", "replace", "default", "dev", iface}
}

func runIP(args ...string) error {
	return exec.Command("ip", args...).Run()
}
