BINARY := llm
GOBIN := $(shell go env GOPATH)/bin

build:
	go build -o $(BINARY) .

install: build
	cp $(BINARY) $(GOBIN)/$(BINARY)
	codesign --force --sign - $(GOBIN)/$(BINARY)
	@echo "Installed $(BINARY) to $(GOBIN)/$(BINARY)"

completions: install
	$(GOBIN)/$(BINARY) completion fish > ~/.config/fish/completions/$(BINARY).fish

clean:
	rm -f $(BINARY)
