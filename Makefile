BINARY := dotllm
COMPAT := llm
GOBIN := $(shell go env GOPATH)/bin

.PHONY: build install completions clean
build:
	go build -o $(BINARY) .

# Build one artifact and expose the legacy spelling as a relative symlink.
# Refuse to replace an unrelated compatibility executable without owner action.
install: build
	@if [ -e "$(GOBIN)/$(COMPAT)" ] || [ -L "$(GOBIN)/$(COMPAT)" ]; then \
		if [ "$$(readlink "$(GOBIN)/$(COMPAT)")" != "$(BINARY)" ]; then \
			echo "Refusing to replace $(GOBIN)/$(COMPAT); preserve it manually first." >&2; exit 1; \
		fi; \
	fi
	mkdir -p "$(GOBIN)"
	cp "$(BINARY)" "$(GOBIN)/$(BINARY)"
	codesign --force --sign - "$(GOBIN)/$(BINARY)"
	@if [ ! -L "$(GOBIN)/$(COMPAT)" ]; then ln -s "$(BINARY)" "$(GOBIN)/$(COMPAT)"; fi
	@echo "Installed $(BINARY) and $(COMPAT) to $(GOBIN)"

completions: install
	"$(GOBIN)/$(BINARY)" completion fish > ~/.config/fish/completions/$(BINARY).fish
	"$(GOBIN)/$(COMPAT)" completion fish > ~/.config/fish/completions/$(COMPAT).fish

clean:
	rm -f "$(BINARY)"
