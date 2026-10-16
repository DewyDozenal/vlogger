BIN_DIR    ?= $(HOME)/.local/bin
PLUGIN_DIR ?= $(HOME)/.config/omarchy/plugins/blake.webcam-recorder

.PHONY: build test install-bin install

build:
	go build -o bin/vlogger ./cmd/vlogger
	go build -o bin/vlogger-gui ./cmd/vlogger-gui

test:
	go test ./...

# Both binaries, for quick test-and-iterate cycles. The GUI window is
# floated by a Hyprland window rule in ~/.config/hypr/hyprland.lua.
install-bin: build
	install -Dm755 bin/vlogger $(BIN_DIR)/vlogger
	install -Dm755 bin/vlogger-gui $(BIN_DIR)/vlogger-gui

# Binaries plus the Omarchy bar-widget plugin.
install: install-bin
	install -d $(PLUGIN_DIR)
	install -m644 plugin/blake.webcam-recorder/* $(PLUGIN_DIR)/
