// Command vlogger records only the webcam, with no screen capture.
//
//	vlogger start    begin recording in the background
//	vlogger stop     finish the recording and print its path
//	vlogger toggle   start if idle, stop if recording
//	vlogger status   exit 0 while recording, 1 otherwise
//	vlogger devices  list webcam capture devices
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"vlogger/internal/recorder"
)

// exitError makes a command exit with a status code without printing a message.
type exitError int

func (e exitError) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

const usage = `usage: vlogger <command> [flags]

commands:
  start      begin webcam recording in the background
  stop       stop the active recording and print the file path
  toggle     start if idle, stop if recording (accepts start's flags)
  status     exit 0 if recording, 1 if idle (--json for machine output)
  devices    list webcam capture devices

run "vlogger <command> -h" for a command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "start":
		err = cmdStart(args)
	case "stop":
		err = cmdStop(args)
	case "toggle":
		err = cmdToggle(args)
	case "status":
		err = cmdStatus(args)
	case "devices":
		err = cmdDevices(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "vlogger: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err == nil {
		return
	}
	var code exitError
	if errors.As(err, &code) {
		os.Exit(int(code))
	}
	fmt.Fprintln(os.Stderr, "vlogger:", err)
	os.Exit(1)
}

// recordFlags defines the options shared by start and toggle.
func recordFlags(name string) (*flag.FlagSet, *recorder.Options) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	defaults := recorder.DefaultOptions()
	o := &defaults
	fs.StringVar(&o.Device, "device", o.Device, "V4L2 device, e.g. /dev/video0 (default: first webcam)")
	fs.StringVar(&o.Size, "size", o.Size, "capture size WxH")
	fs.IntVar(&o.FPS, "fps", o.FPS, "frames per second")
	fs.StringVar(&o.InputFormat, "input-format", o.InputFormat,
		`v4l2 pixel format: mjpeg, yuyv422, or "" for ffmpeg to choose`)
	fs.BoolVar(&o.Mic, "mic", o.Mic, "also record the default microphone")
	fs.StringVar(&o.Dir, "dir", o.Dir, "output directory (default: XDG videos directory)")
	return fs, o
}

func parseRecordFlags(name string, args []string) (*recorder.Options, error) {
	fs, o := recordFlags(name)
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return o, nil
}

func cmdStart(args []string) error {
	o, err := parseRecordFlags("start", args)
	if err != nil {
		return err
	}
	st, err := recorder.Start(*o, time.Now())
	if errors.Is(err, recorder.ErrAlreadyRecording) {
		return fmt.Errorf("%w to %s (pid %d)", err, st.Output, st.PID)
	}
	if err != nil {
		return err
	}
	fmt.Printf("recording %s from %s\n", st.Output, st.Device)
	return nil
}

func cmdStop(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("stop takes no arguments")
	}
	return stopRecording()
}

func stopRecording() error {
	st, err := recorder.Stop()
	if errors.Is(err, recorder.ErrNotRecording) {
		fmt.Fprintln(os.Stderr, "vlogger: not recording")
		return exitError(1)
	}
	if errors.Is(err, recorder.ErrForced) {
		fmt.Println(st.Output)
		return err
	}
	if err != nil {
		return err
	}
	fmt.Println(st.Output)
	return nil
}

func cmdToggle(args []string) error {
	active, err := recorder.Current()
	if err != nil {
		return err
	}
	if active != nil {
		return stopRecording()
	}
	return cmdStart(args)
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	active, err := recorder.Current()
	if err != nil {
		return err
	}

	if *asJSON {
		out := struct {
			Recording bool            `json:"recording"`
			Session   *recorder.State `json:"session,omitempty"`
		}{Recording: active != nil, Session: active}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			return err
		}
	} else if active == nil {
		fmt.Println("idle")
	} else {
		elapsed := time.Since(active.Started).Round(time.Second)
		fmt.Printf("recording %s for %s\n", active.Output, elapsed)
	}

	if active == nil {
		return exitError(1)
	}
	return nil
}

func cmdDevices(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("devices takes no arguments")
	}
	devices, err := recorder.SystemDevices()
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		return errors.New("no webcam devices found")
	}
	fmt.Print(recorder.FormatDevices(devices))
	return nil
}
