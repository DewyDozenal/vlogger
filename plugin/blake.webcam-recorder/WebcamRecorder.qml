import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui

// Bar button that starts and stops webcam-only recording via the `vlogger`
// CLI. The Go binary owns all state; this widget only asks it whether a
// recording is running and tells it to toggle.
BarWidget {
  id: root
  moduleName: "blake.webcam-recorder"

  property bool recording: false

  function refresh() {
    if (!statusProc.running) statusProc.running = true
  }

  function toggle() {
    if (root.bar) root.bar.run("vlogger toggle")
    settle.restart()
  }

  visible: true
  implicitWidth: button.implicitWidth
  implicitHeight: button.implicitHeight

  IpcHandler {
    target: "blake.webcam-recorder"

    function refresh(): void { root.refresh() }
    function toggle(): void { root.toggle() }
  }

  // `vlogger status` exits 0 while recording and 1 while idle.
  Process {
    id: statusProc
    command: ["vlogger", "status"]
    onExited: function(exitCode) {
      root.recording = exitCode === 0
    }
  }

  Timer {
    interval: 2000
    running: true
    repeat: true
    triggeredOnStart: true
    onTriggered: root.refresh()
  }

  // Re-check shortly after a toggle so the icon flips without waiting for
  // the next poll.
  Timer {
    id: settle
    interval: 700
    onTriggered: root.refresh()
  }

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: "\uf03d"
    active: root.recording
    tooltipText: root.recording ? "Stop webcam recording" : "Start webcam recording"
    onPressed: function(b) {
      root.toggle()
    }
  }
}
