BINARY      := paste
PREFIX      := /usr/local
BIN_DIR     := $(PREFIX)/bin
VAR_DIR     := $(PREFIX)/var/paste
LOG_DIR     := $(PREFIX)/var/log
PLIST_SRC   := launchd/com.rishabh.paste.plist
PLIST_DST   := /Library/LaunchDaemons/com.rishabh.paste.plist
LABEL       := com.rishabh.paste

.PHONY: build install uninstall reload logs status hosts

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) .

install: build
	@[ "$$(id -u)" = "0" ] || (echo "run with sudo" && exit 1)
	install -d $(BIN_DIR) $(VAR_DIR) $(LOG_DIR)
	install -m 0755 $(BINARY) $(BIN_DIR)/$(BINARY)
	install -m 0644 $(PLIST_SRC) $(PLIST_DST)
	launchctl bootout system/$(LABEL) 2>/dev/null || true
	launchctl bootstrap system $(PLIST_DST)
	launchctl enable system/$(LABEL)
	@echo
	@echo "installed. test with:  curl http://127.0.0.1/healthz"
	@echo "add hostname with:     sudo make hosts"

uninstall:
	@[ "$$(id -u)" = "0" ] || (echo "run with sudo" && exit 1)
	launchctl bootout system/$(LABEL) 2>/dev/null || true
	rm -f $(PLIST_DST)
	rm -f $(BIN_DIR)/$(BINARY)
	@echo "uninstalled. database at $(VAR_DIR) left intact."

reload:
	@[ "$$(id -u)" = "0" ] || (echo "run with sudo" && exit 1)
	launchctl bootout system/$(LABEL) 2>/dev/null || true
	launchctl bootstrap system $(PLIST_DST)

logs:
	tail -F $(LOG_DIR)/paste.log

status:
	@launchctl print system/$(LABEL) 2>/dev/null | grep -E '^\s*(state|pid|last exit code)' || echo "not loaded"

hosts:
	@[ "$$(id -u)" = "0" ] || (echo "run with sudo" && exit 1)
	@grep -q "^127\.0\.0\.1[[:space:]]\+paste$$" /etc/hosts || echo "127.0.0.1 paste" >> /etc/hosts
	@echo "/etc/hosts entry ensured. open http://paste/ in your browser."
