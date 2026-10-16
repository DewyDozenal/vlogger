// Command vlogger-gui is a small window with a start/stop button and a live
// preview of the webcam while a recording is active.
//
// It is only a front end. Recording is done by the same session logic the
// `vlogger` CLI uses, so closing this window does not stop a recording, and
// reopening it shows the one in progress.
package main

import (
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decodes the preview frames written by ffmpeg
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"vlogger/internal/recorder"
)

// appID and windowTitle are what the Hyprland window rule matches on, so
// keep them in sync with the rule in ~/.config/hypr/hyprland.lua.
const (
	appID       = "vlogger-gui"
	windowTitle = "Webcam Recorder"
	pollEvery   = 100 * time.Millisecond
)

type gui struct {
	win         fyne.Window
	preview     *canvas.Image
	placeholder *widget.Label
	status      *widget.Label
	camera      *widget.Select
	button      *widget.Button

	// devices are the webcams found at startup, in the same order as the
	// camera select's options.
	devices []recorder.Device

	// busy is set while a start or stop is in flight, so the button cannot
	// queue a second action. Start blocks about a second while ffmpeg settles.
	busy atomic.Bool

	// Only the poll goroutine touches these, so no lock is needed.
	shownFrame   time.Time
	shownSize    int64
	lastStatus   string
	wasRecording bool
}

func main() {
	a := app.NewWithID(appID)
	w := a.NewWindow(windowTitle)

	g := newGUI(w)
	w.SetContent(g.layout())
	w.Resize(fyne.NewSize(640, 480))
	w.SetMaster()

	go g.poll()
	w.ShowAndRun()
}

func newGUI(w fyne.Window) *gui {
	g := &gui{win: w}

	g.preview = canvas.NewImageFromImage(nil)
	g.preview.FillMode = canvas.ImageFillContain
	g.preview.SetMinSize(fyne.NewSize(320, 180))
	g.preview.Hide()

	g.placeholder = widget.NewLabelWithStyle("Not recording", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})

	g.status = widget.NewLabelWithStyle("Idle", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	g.camera = widget.NewSelect(nil, nil)
	g.camera.PlaceHolder = "No webcam found"
	g.loadCameras()

	g.button = widget.NewButton("Start recording", g.toggle)
	g.button.Importance = widget.HighImportance

	return g
}

// loadCameras fills the camera select with the webcams the kernel exposes
// now. The list is read once at startup, so a camera plugged in later needs
// the window to be reopened.
func (g *gui) loadCameras() {
	devices, err := recorder.SystemDevices()
	if err != nil {
		g.status.SetText("Error: " + err.Error())
	}
	g.devices = devices

	labels := make([]string, len(devices))
	for i, d := range devices {
		labels[i] = cameraLabel(d)
	}
	g.camera.SetOptions(labels)
	if len(labels) > 0 {
		g.camera.SetSelectedIndex(0)
	}
	g.lockCamera(false)
}

// lockCamera enables or disables the camera select. It stays disabled when
// there is no camera to choose, so the placeholder is never offered as a
// working control.
func (g *gui) lockCamera(locked bool) {
	if locked || len(g.devices) == 0 {
		g.camera.Disable()
		return
	}
	g.camera.Enable()
}

// cameraLabel names a camera in the select. The path is included because two
// identical USB cameras report the same driver name.
func cameraLabel(d recorder.Device) string {
	if d.Name == "" {
		return d.Path
	}
	return fmt.Sprintf("%s (%s)", d.Name, d.Path)
}

// selectedDevice returns the path of the camera chosen in the select, or ""
// if none is available.
func (g *gui) selectedDevice() string {
	i := g.camera.SelectedIndex()
	if i < 0 || i >= len(g.devices) {
		return ""
	}
	return g.devices[i].Path
}

func (g *gui) layout() fyne.CanvasObject {
	// Black behind the preview makes letterboxing look intentional.
	backdrop := canvas.NewRectangle(color.Black)
	body := container.NewStack(
		backdrop,
		container.NewCenter(g.placeholder),
		g.preview,
	)
	top := container.NewVBox(g.status, g.camera)
	return container.NewBorder(top, g.button, nil, nil, body)
}

// toggle starts a recording if idle or stops the active one. Both calls block
// while ffmpeg starts or finishes, so they run off the UI thread.
//
// The camera select is locked while a session runs, because the device is
// fixed for the life of a recording. It is read here on the UI thread so the
// goroutine records from the camera selected when the button was pressed.
func (g *gui) toggle() {
	if !g.busy.CompareAndSwap(false, true) {
		return
	}
	g.button.Disable()
	g.camera.Disable()
	device := g.selectedDevice()

	go func() {
		active, err := recorder.Current()
		if err == nil {
			if active != nil {
				_, err = recorder.Stop()
			} else {
				opts := recorder.DefaultOptions()
				opts.Device = device
				_, err = recorder.Start(opts, time.Now())
			}
		}

		fyne.Do(func() {
			g.busy.Store(false)
			g.button.Enable()
			if err != nil {
				g.status.SetText("Error: " + err.Error())
				g.lastStatus = ""
				g.lockCamera(false)
			}
		})
	}()
}

// poll keeps the window in sync with the recorder. It reads the session state
// and the preview frame in the background and applies the result on the UI
// thread.
func (g *gui) poll() {
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for range ticker.C {
		if g.busy.Load() {
			continue
		}
		g.sync()
	}
}

func (g *gui) sync() {
	active, err := recorder.Current()
	if err != nil {
		return
	}

	var frame image.Image
	if active != nil {
		frame = g.loadFrameIfChanged(active.Preview)
	} else {
		g.shownFrame = time.Time{}
		g.shownSize = 0
	}

	recording := active != nil
	var elapsed time.Duration
	var file string
	if recording {
		elapsed = time.Since(active.Started).Round(time.Second)
		file = filepath.Base(active.Output)
	}

	fyne.Do(func() {
		g.apply(recording, elapsed, file, frame)
	})
}

// loadFrameIfChanged decodes the preview only when ffmpeg has rewritten it.
// It returns nil when there is nothing new to show.
func (g *gui) loadFrameIfChanged(path string) image.Image {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.ModTime().Equal(g.shownFrame) && info.Size() == g.shownSize {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	g.shownFrame = info.ModTime()
	g.shownSize = info.Size()
	return img
}

// apply runs on the UI thread.
func (g *gui) apply(recording bool, elapsed time.Duration, file string, frame image.Image) {
	if !recording {
		if g.wasRecording {
			g.preview.Image = nil
			g.preview.Hide()
			g.placeholder.SetText("Not recording")
			g.placeholder.Show()
			g.button.SetText("Start recording")
			g.button.Importance = widget.HighImportance
			g.button.Refresh()
			g.lockCamera(false)
			g.wasRecording = false
		}
		g.setStatus("Idle")
		return
	}

	if !g.wasRecording {
		g.wasRecording = true
		g.button.SetText("Stop recording")
		g.button.Importance = widget.DangerImportance
		g.button.Refresh()
		g.lockCamera(true)
	}

	if frame != nil {
		g.preview.Image = frame
		g.preview.Refresh()
		g.preview.Show()
		g.placeholder.Hide()
	} else if g.preview.Image == nil {
		g.placeholder.SetText("Starting camera...")
		g.placeholder.Show()
	}

	g.setStatus(fmt.Sprintf("REC %s  %s", formatElapsed(elapsed), file))
}

// setStatus updates the label only when the text changes, so the once-a-tick
// poll does not trigger a redraw every time.
func (g *gui) setStatus(text string) {
	if text == g.lastStatus {
		return
	}
	g.lastStatus = text
	g.status.SetText(text)
}

func formatElapsed(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}
