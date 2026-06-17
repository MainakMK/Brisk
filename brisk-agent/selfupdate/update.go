package selfupdate

import (
	"fmt"
	"os"
)

// Paths are the on-disk locations the self-updater touches. Configurable so tests can use a
// temp dir; production uses DefaultPaths().
type Paths struct {
	Binary string // the live agent binary (what systemd ExecStart points at)
	Prev   string // backup of the previous binary, for one-step rollback
	New    string // staging path for a freshly-downloaded binary before the swap
	Marker string // "update in progress" marker: holds "<version>\n<restartCount>"
}

// DefaultPaths returns the production layout: /usr/local/bin/brisk-agent + siblings,
// and a marker under /etc/brisk so it survives the binary swap.
func DefaultPaths() Paths {
	b := "/usr/local/bin/brisk-agent"
	return Paths{Binary: b, Prev: b + ".prev", New: b + ".new", Marker: "/etc/brisk/update.inprogress"}
}

// Apply installs an already-VERIFIED binary: stage → keep .prev → drop the marker → atomic swap.
// data MUST have passed VerifyBinary first. Returns shouldExit=true on success so the caller can
// exit and let systemd (Restart=always) relaunch into the new binary.
func Apply(p Paths, data []byte, targetVersion string) (shouldExit bool, err error) {
	if err = os.WriteFile(p.New, data, 0o755); err != nil {
		return false, fmt.Errorf("write new binary: %w", err)
	}
	cur, err := os.ReadFile(p.Binary)
	if err != nil {
		return false, fmt.Errorf("read current binary: %w", err)
	}
	if err = os.WriteFile(p.Prev, cur, 0o755); err != nil {
		return false, fmt.Errorf("write .prev backup: %w", err)
	}
	if err = os.WriteFile(p.Marker, []byte(targetVersion+"\n0"), 0o644); err != nil {
		return false, fmt.Errorf("write marker: %w", err)
	}
	if err = os.Rename(p.New, p.Binary); err != nil { // atomic on the same filesystem
		return false, fmt.Errorf("swap in new binary: %w", err)
	}
	return true, nil
}

// SelfCheckOnStart runs at every boot. If a marker exists, this process is a freshly-swapped
// binary on trial: it runs check(); on success it commits (removes the marker); on failure it
// bumps a restart counter, and once that reaches maxRestarts it restores .prev and returns
// rolledBack=true so the caller exits — systemd then relaunches the restored OLD binary.
//
// This makes self-update safe even for a totally broken build (one that can't even start),
// WITHOUT needing the control plane: the marker + restart count drive a local auto-rollback.
func SelfCheckOnStart(p Paths, maxRestarts int, check func() error) (rolledBack bool) {
	data, err := os.ReadFile(p.Marker)
	if err != nil {
		return false // no update in progress — normal boot
	}
	var version string
	var n int
	fmt.Sscanf(string(data), "%s\n%d", &version, &n)

	if err := check(); err == nil {
		_ = os.Remove(p.Marker) // commit
		return false
	}
	if n+1 >= maxRestarts {
		if prev, e := os.ReadFile(p.Prev); e == nil {
			_ = os.WriteFile(p.Binary, prev, 0o755)
		}
		_ = os.Remove(p.Marker)
		return true // caller exits → systemd starts the restored old binary
	}
	_ = os.WriteFile(p.Marker, []byte(fmt.Sprintf("%s\n%d", version, n+1)), 0o644)
	return false
}

// RestartSelf exits cleanly so systemd (Restart=always) relaunches the binary on disk.
func RestartSelf() { os.Exit(0) }
