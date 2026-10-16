package recorder

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgsVideoOnly(t *testing.T) {
	o := Options{Device: "/dev/video0", Size: "1280x720", FPS: 30, InputFormat: "mjpeg"}
	got := BuildArgs(o, "/out/a.mp4", "/run/preview.jpg")

	want := []string{
		"-hide_banner", "-nostdin", "-loglevel", "warning", "-f", "v4l2",
		"-input_format", "mjpeg",
		"-video_size", "1280x720", "-framerate", "30", "-i", "/dev/video0",
		"-map", "0:v:0",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"/out/a.mp4",
		"-map", "0:v:0",
		"-vf", "fps=10,scale=640:-2",
		"-c:v", "mjpeg", "-q:v", "5",
		"-f", "image2", "-update", "1", "-atomic_writing", "1",
		"/run/preview.jpg",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs video-only mismatch\n got: %q\nwant: %q", got, want)
	}
	if strings.Contains(strings.Join(got, " "), "pulse") {
		t.Fatal("video-only recording must not open an audio source")
	}
}

func TestBuildArgsWithMicAndNoInputFormat(t *testing.T) {
	o := Options{Device: "/dev/video2", Size: "640x480", FPS: 15, Mic: true}
	got := strings.Join(BuildArgs(o, "/out/b.mp4", "/run/p.jpg"), " ")

	for _, frag := range []string{
		"-f pulse -i default",
		"-map 1:a:0 -c:a aac -b:a 128k",
		"-video_size 640x480 -framerate 15 -i /dev/video2",
	} {
		if !strings.Contains(got, frag) {
			t.Errorf("expected %q in args: %s", frag, got)
		}
	}
	if strings.Contains(got, "-input_format") {
		t.Errorf("empty InputFormat must omit -input_format: %s", got)
	}
	// Audio belongs to the recording only; the preview must stay video-only.
	previewStart := strings.Index(got, "-vf fps=10")
	if previewStart < 0 || strings.Contains(got[previewStart:], "1:a:0") {
		t.Errorf("preview output must not map audio: %s", got)
	}
}

func TestOptionsValidate(t *testing.T) {
	tests := []struct {
		name    string
		o       Options
		wantErr bool
	}{
		{"valid", Options{Size: "1280x720", FPS: 30}, false},
		{"size missing x", Options{Size: "1280", FPS: 30}, true},
		{"size with letters", Options{Size: "720p", FPS: 30}, true},
		{"size injection attempt", Options{Size: "1280x720;rm", FPS: 30}, true},
		{"fps zero", Options{Size: "640x480", FPS: 0}, true},
		{"fps too high", Options{Size: "640x480", FPS: 500}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.o.validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestListDevicesPrimaryNodesOnly(t *testing.T) {
	root := t.TempDir()
	// Capture node for camera A, its metadata node, and camera B's capture node.
	// video10 is included to check numeric (not lexical) ordering.
	fakeNode(t, root, "video0", "0", "USB Camera A")
	fakeNode(t, root, "video1", "1", "USB Camera A")
	fakeNode(t, root, "video10", "0", "Laptop Cam")
	fakeNode(t, root, "video2", "0", "Second Cam")

	got, err := ListDevices(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Device{
		{Path: "/dev/video0", Name: "USB Camera A"},
		{Path: "/dev/video2", Name: "Second Cam"},
		{Path: "/dev/video10", Name: "Laptop Cam"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListDevices\n got: %+v\nwant: %+v", got, want)
	}
}

func TestListDevicesEmpty(t *testing.T) {
	got, err := ListDevices(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no devices, got %+v", got)
	}
}

func fakeNode(t *testing.T, root, name, index, devName string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index"), []byte(index+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "name"), []byte(devName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsFFmpegRejectsOtherProcesses(t *testing.T) {
	if isFFmpeg(os.Getpid()) {
		t.Fatal("the test binary is not ffmpeg")
	}
	if isFFmpeg(0) || isFFmpeg(-1) {
		t.Fatal("invalid pids must not be reported as ffmpeg")
	}
}

func TestIsFFmpegAcceptsRealFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "quiet", "-f", "lavfi", "-i", "nullsrc=d=5", "-f", "null", "-")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	if !isFFmpeg(cmd.Process.Pid) {
		t.Fatal("expected a running ffmpeg to be detected")
	}
}

func TestStateRoundTripAndStaleCleanup(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	// No state yet.
	if st, err := Current(); err != nil || st != nil {
		t.Fatalf("Current() with no state = %+v, %v", st, err)
	}

	// State pointing at a dead process is treated as idle and removed.
	if err := saveState(State{PID: 0, Output: "/x.mp4"}); err != nil {
		t.Fatal(err)
	}
	if st, err := Current(); err != nil || st != nil {
		t.Fatalf("stale state should be ignored, got %+v, %v", st, err)
	}
	if _, err := os.Stat(statePath()); !os.IsNotExist(err) {
		t.Fatal("stale state file should have been removed")
	}

	// Corrupt state is also cleared rather than wedging the CLI.
	if err := os.MkdirAll(runtimeDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if st, err := Current(); err != nil || st != nil {
		t.Fatalf("corrupt state should be ignored, got %+v, %v", st, err)
	}
}

func TestStopWithoutSession(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if _, err := Stop(); err != ErrNotRecording {
		t.Fatalf("Stop() err = %v, want ErrNotRecording", err)
	}
}

func TestDefaultOutputDirFromUserDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(home, ".config")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without user-dirs.dirs the default is ~/Videos.
	if got := DefaultOutputDir(); got != filepath.Join(home, "Videos") {
		t.Fatalf("fallback = %q", got)
	}

	content := "# comment\nXDG_DOCUMENTS_DIR=\"$HOME/Docs\"\nXDG_VIDEOS_DIR=\"$HOME/Clips\"\n"
	if err := os.WriteFile(filepath.Join(cfg, "user-dirs.dirs"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DefaultOutputDir(); got != filepath.Join(home, "Clips") {
		t.Fatalf("user-dirs value = %q, want %q", got, filepath.Join(home, "Clips"))
	}
}
