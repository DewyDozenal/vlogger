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
	button      *widget.Button

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

	g.button = widget.NewButton("Start recording", g.toggle)
	g.button.Importance = widget.HighImportance

	return g
}

func (g *gui) layout() fyne.CanvasObject {
	// Black behind the preview makes letterboxing look intentional.
	backdrop := canvas.NewRectangle(color.Black)
	body := container.NewStack(
		backdrop,
		container.NewCenter(g.placeholder),
		g.preview,
	)
	return container.NewBorder(g.status, g.button, nil, nil, body)
}

// toggle starts a recording if idle or stops the active one. Both calls block
// while ffmpeg starts or finishes, so they run off the UI thread.
func (g *gui) toggle() {
	if !g.busy.CompareAndSwap(false, true) {
		return
	}
	g.button.Disable()

	go func() {
		active, err := recorder.Current()
		if err == nil {
			if active != nil {
				_, err = recorder.Stop()
			} else {
				_, err = recorder.Start(recorder.DefaultOptions(), time.Now())
			}
		}

		fyne.Do(func() {
			g.busy.Store(false)
			g.button.Enable()
			if err != nil {
				g.status.SetText("Error: " + err.Error())
				g.lastStatus = ""
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
