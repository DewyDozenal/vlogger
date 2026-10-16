package main

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"vlogger/internal/recorder"
)

// TestButtonStartsAndStopsRecording taps the real button in Fyne's headless
// test driver. It uses the real webcam, so it only runs when
// VLOGGER_CAMERA_TEST=1 is set.
func TestButtonStartsAndStopsRecording(t *testing.T) {
	if os.Getenv("VLOGGER_CAMERA_TEST") != "1" {
		t.Skip("set VLOGGER_CAMERA_TEST=1 to record from the real webcam")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// Isolate state and output so the test never touches a real session.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("test")
	g := newGUI(w)
	w.SetContent(g.layout())

	test.Tap(g.button)
	waitUntil(t, 15*time.Second, func() bool {
		st, _ := recorder.Current()
		return st != nil && !g.busy.Load()
	}, "recording to start")

	g.sync()
	if got := g.button.Text; got != "Stop recording" {
		t.Fatalf("button text while recording = %q, want %q", got, "Stop recording")
	}

	test.Tap(g.button)
	waitUntil(t, 15*time.Second, func() bool {
		st, _ := recorder.Current()
		return st == nil && !g.busy.Load()
	}, "recording to stop")

	g.sync()
	if got := g.button.Text; got != "Start recording" {
		t.Fatalf("button text while idle = %q, want %q", got, "Start recording")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
