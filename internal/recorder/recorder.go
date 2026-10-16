// Package recorder records a V4L2 webcam to an MP4 file with ffmpeg.
//
// Only the camera is captured: there is no screen grab. A background ffmpeg
// process is started in its own session so it survives the CLI exiting, and
// its PID is kept in a state file so later invocations can find and stop it.
package recorder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Options configures one recording session.
type Options struct {
	Device      string // V4L2 node, e.g. /dev/video0. Empty selects the first webcam.
	Size        string // WxH, e.g. 1280x720
	FPS         int    // frames per second
	InputFormat string // v4l2 pixel format (mjpeg, yuyv422). Empty lets ffmpeg choose.
	Mic         bool   // also record the default PulseAudio/PipeWire source
	Dir         string // directory the recording is written into
}

var (
	// ErrAlreadyRecording is returned by Start when a session is active.
	ErrAlreadyRecording = errors.New("already recording")
	// ErrNotRecording is returned by Stop when no session is active.
	ErrNotRecording = errors.New("not recording")
	// ErrForced is returned by Stop when ffmpeg ignored SIGINT and was killed.
	ErrForced = errors.New("ffmpeg did not exit in time and was killed; the file may be incomplete")
)

const (
	// startupGrace is how long ffmpeg must survive before Start reports success.
	// Device-busy and unsupported-format errors show up within this window.
	startupGrace = time.Second
	stopTimeout  = 10 * time.Second
)

// DefaultOptions is the configuration used when a caller does not override it.
func DefaultOptions() Options {
	return Options{
		Size:        "1280x720",
		FPS:         30,
		InputFormat: "mjpeg",
		Dir:         DefaultOutputDir(),
	}
}

var sizePattern = regexp.MustCompile(`^[0-9]+x[0-9]+$`)

func (o Options) validate() error {
	if !sizePattern.MatchString(o.Size) {
		return fmt.Errorf("invalid size %q (expected WxH, e.g. 1280x720)", o.Size)
	}
	if o.FPS < 1 || o.FPS > 120 {
		return fmt.Errorf("invalid fps %d (expected 1-120)", o.FPS)
	}
	return nil
}

// BuildArgs returns the ffmpeg argument list for a session. It has two
// outputs: the recording itself, and a small JPEG preview that is rewritten
// about ten times a second so a GUI can show what is being captured.
//
// The recording is fragmented MP4, so a file stays playable even if the
// process is killed.
func BuildArgs(o Options, output, preview string) []string {
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning", "-f", "v4l2"}
	if o.InputFormat != "" {
		args = append(args, "-input_format", o.InputFormat)
	}
	args = append(args,
		"-video_size", o.Size,
		"-framerate", strconv.Itoa(o.FPS),
		"-i", o.Device,
	)
	if o.Mic {
		args = append(args, "-f", "pulse", "-i", "default")
	}

	// Output 1: the recording.
	args = append(args, "-map", "0:v:0")
	if o.Mic {
		args = append(args, "-map", "1:a:0", "-c:a", "aac", "-b:a", "128k")
	}
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-pix_fmt", "yuv420p",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		output,
	)

	// Output 2: preview. -atomic_writing renames each frame into place, so a
	// reader never sees a half-written JPEG.
	args = append(args,
		"-map", "0:v:0",
		"-vf", "fps=10,scale=640:-2",
		"-c:v", "mjpeg", "-q:v", "5",
		"-f", "image2", "-update", "1", "-atomic_writing", "1",
		preview,
	)
	return args
}

// Start begins a recording and returns once ffmpeg has survived startup.
// If a session is already active, it returns that session along with
// ErrAlreadyRecording.
func Start(o Options, now time.Time) (State, error) {
	if err := o.validate(); err != nil {
		return State{}, err
	}

	active, err := Current()
	if err != nil {
		return State{}, err
	}
	if active != nil {
		return *active, ErrAlreadyRecording
	}

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return State{}, errors.New("ffmpeg not found in PATH; install the ffmpeg package")
	}

	if o.Device == "" {
		devices, err := ListDevices(sysVideoRoot)
		if err != nil {
			return State{}, err
		}
		if len(devices) == 0 {
			return State{}, errors.New("no webcam found; check `vlogger devices`")
		}
		o.Device = devices[0].Path
	}
	if _, err := os.Stat(o.Device); err != nil {
		return State{}, fmt.Errorf("camera %s: %w", o.Device, err)
	}

	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return State{}, fmt.Errorf("output directory: %w", err)
	}
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return State{}, fmt.Errorf("state directory: %w", err)
	}

	logFile, err := os.Create(logPath())
	if err != nil {
		return State{}, fmt.Errorf("ffmpeg log: %w", err)
	}
	defer logFile.Close()

	if err := os.MkdirAll(runtimeDir(), 0o700); err != nil {
		return State{}, fmt.Errorf("runtime directory: %w", err)
	}
	preview := PreviewPath()
	_ = os.Remove(preview) // drop a frame left over from a previous session

	output := filepath.Join(o.Dir, "webcam-"+now.Format("2006-01-02_15-04-05")+".mp4")

	cmd := exec.Command(ffmpeg, BuildArgs(o, output, preview)...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// A new session keeps ffmpeg out of this process's terminal and signal
	// group, so Ctrl-C in the shell that ran `vlogger start` does not stop it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return State{}, fmt.Errorf("starting ffmpeg: %w", err)
	}

	// Reap the child in the background so an early exit is observable.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	select {
	case err := <-exited:
		return State{}, fmt.Errorf("ffmpeg exited during startup (%v); see %s", err, logPath())
	case <-time.After(startupGrace):
	}

	st := State{PID: cmd.Process.Pid, Output: output, Device: o.Device, Started: now, Preview: preview}
	if err := saveState(st); err != nil {
		_ = syscall.Kill(st.PID, syscall.SIGKILL)
		return State{}, err
	}
	return st, nil
}

// Stop ends the active session. ffmpeg is sent SIGINT so it can write the
// MP4 trailer. It is killed only if it fails to exit within the timeout.
func Stop() (State, error) {
	st, err := Current()
	if err != nil {
		return State{}, err
	}
	if st == nil {
		return State{}, ErrNotRecording
	}

	_ = syscall.Kill(st.PID, syscall.SIGINT)

	deadline := time.Now().Add(stopTimeout)
	for isFFmpeg(st.PID) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(st.PID, syscall.SIGKILL)
			removeSession(*st)
			return *st, ErrForced
		}
		time.Sleep(100 * time.Millisecond)
	}

	removeSession(*st)
	return *st, nil
}

// removeSession deletes the state file and the preview frame of a finished session.
func removeSession(st State) {
	_ = os.Remove(statePath())
	_ = os.Remove(st.Preview)
}

// Current returns the active session, or nil if none is running. A state
// file whose process is gone (reboot, crash, manual kill) is removed.
func Current() (*State, error) {
	b, err := os.ReadFile(statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state: %w", err)
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil || !isFFmpeg(st.PID) {
		_ = os.Remove(statePath())
		return nil, nil
	}
	return &st, nil
}

// isFFmpeg reports whether pid is a live (non-zombie) ffmpeg process. The
// check guards against a recycled PID pointing at an unrelated program.
func isFFmpeg(pid int) bool {
	if pid <= 0 {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// Format: "pid (comm) state ...". comm may itself contain spaces or parens,
	// so split on the last ')'.
	s := string(b)
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < open {
		return false
	}
	comm := s[open+1 : closeIdx]
	fields := strings.Fields(s[closeIdx+1:])
	if len(fields) == 0 || fields[0] == "Z" || fields[0] == "X" {
		return false
	}
	return comm == "ffmpeg"
}
