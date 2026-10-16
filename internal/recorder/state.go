package recorder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State describes the active recording. It is stored as JSON under the
// runtime directory so that separate `vlogger` invocations can see it.
type State struct {
	PID     int       `json:"pid"`
	Output  string    `json:"output"`
	Device  string    `json:"device"`
	Started time.Time `json:"started"`
	Preview string    `json:"preview"`
}

// runtimeDir holds the state file. It is per-login and cleared on logout.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "vlogger")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("vlogger-%d", os.Getuid()))
}

// stateDir holds logs. Unlike the runtime dir, it persists across reboots.
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "vlogger")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "vlogger")
}

func statePath() string { return filepath.Join(runtimeDir(), "state.json") }

// PreviewPath is the JPEG frame rewritten while a recording is active.
func PreviewPath() string { return filepath.Join(runtimeDir(), "preview.jpg") }
func logPath() string     { return filepath.Join(stateDir(), "ffmpeg.log") }

func saveState(st State) error {
	if err := os.MkdirAll(runtimeDir(), 0o700); err != nil {
		return fmt.Errorf("state directory: %w", err)
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// Write-then-rename so a reader never sees a partial file.
	tmp := statePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	return os.Rename(tmp, statePath())
}

// DefaultOutputDir returns XDG_VIDEOS_DIR from ~/.config/user-dirs.dirs, or
// ~/Videos when that file is missing or does not set it.
func DefaultOutputDir() string {
	home, _ := os.UserHomeDir()
	fallback := filepath.Join(home, "Videos")

	f, err := os.Open(filepath.Join(home, ".config", "user-dirs.dirs"))
	if err != nil {
		return fallback
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		value, ok := strings.CutPrefix(line, "XDG_VIDEOS_DIR=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		value = strings.ReplaceAll(value, "$HOME", home)
		if value != "" {
			return value
		}
	}
	return fallback
}
