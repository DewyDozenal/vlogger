package main

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

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

// TestSelectedDeviceFollowsCameraSelect checks that the device passed to
// recorder.Start is the one picked in the camera select. It needs no webcam.
func TestSelectedDeviceFollowsCameraSelect(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()

	devices := []recorder.Device{
		{Path: "/dev/video0", Name: "Integrated Camera"},
		{Path: "/dev/video2", Name: "USB Camera"},
	}
	g := &gui{devices: devices}
	g.camera = widget.NewSelect([]string{cameraLabel(devices[0]), cameraLabel(devices[1])}, nil)
	g.camera.SetSelectedIndex(0)

	if got := g.selectedDevice(); got != "/dev/video0" {
		t.Fatalf("selectedDevice() = %q, want /dev/video0", got)
	}
	g.camera.SetSelectedIndex(1)
	if got := g.selectedDevice(); got != "/dev/video2" {
		t.Fatalf("selectedDevice() after switch = %q, want /dev/video2", got)
	}

	g.camera.ClearSelected()
	if got := g.selectedDevice(); got != "" {
		t.Fatalf("selectedDevice() with nothing selected = %q, want empty", got)
	}
}

func TestCameraLabel(t *testing.T) {
	tests := []struct {
		dev  recorder.Device
		want string
	}{
		{recorder.Device{Path: "/dev/video0", Name: "Integrated Camera"}, "Integrated Camera (/dev/video0)"},
		{recorder.Device{Path: "/dev/video4"}, "/dev/video4"},
	}
	for _, tc := range tests {
		if got := cameraLabel(tc.dev); got != tc.want {
			t.Errorf("cameraLabel(%+v) = %q, want %q", tc.dev, got, tc.want)
		}
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
