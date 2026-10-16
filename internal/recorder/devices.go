package recorder

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// sysVideoRoot is where the kernel exposes V4L2 device nodes.
const sysVideoRoot = "/sys/class/video4linux"

// Device is a V4L2 capture node.
type Device struct {
	Path string // e.g. /dev/video0
	Name string // driver-provided name, e.g. "USB 2.0 Camera: USB Camera"
}

// ListDevices returns the video capture nodes under sysRoot, sorted by number.
//
// A camera usually exposes several /dev/videoN nodes, such as capture plus
// metadata. Only index 0 is the primary capture node, so other nodes are
// skipped.
func ListDevices(sysRoot string) ([]Device, error) {
	dirs, err := filepath.Glob(filepath.Join(sysRoot, "video*"))
	if err != nil {
		return nil, err
	}

	var devices []Device
	for _, dir := range dirs {
		index, err := readTrimmed(filepath.Join(dir, "index"))
		if err != nil || index != "0" {
			continue
		}
		name, _ := readTrimmed(filepath.Join(dir, "name"))
		devices = append(devices, Device{
			Path: filepath.Join("/dev", filepath.Base(dir)),
			Name: name,
		})
	}

	sort.Slice(devices, func(i, j int) bool {
		return videoNumber(devices[i].Path) < videoNumber(devices[j].Path)
	})
	return devices, nil
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// videoNumber extracts N from /dev/videoN so that video2 sorts before video10.
func videoNumber(path string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(filepath.Base(path), "video"))
	if err != nil {
		return int(^uint(0) >> 1)
	}
	return n
}

// FormatDevices renders devices one per line for display.
func FormatDevices(devices []Device) string {
	var b strings.Builder
	for _, d := range devices {
		fmt.Fprintf(&b, "%s\t%s\n", d.Path, d.Name)
	}
	return b.String()
}

// SystemDevices lists the webcams the kernel currently exposes.
func SystemDevices() ([]Device, error) {
	return ListDevices(sysVideoRoot)
}
