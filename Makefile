BINARY  := claudes
PREFIX  ?= $(HOME)/.local
BINDIR  ?= $(PREFIX)/bin
CONFDIR ?= $(or $(XDG_CONFIG_HOME),$(HOME)/.config)/claudes
GO      ?= go

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

ZSH_COMPDIR  ?= $(HOME)/.zsh/completions
BASH_COMPDIR ?= $(HOME)/.local/share/bash-completion/completions
FISH_COMPDIR ?= $(HOME)/.config/fish/completions

.PHONY: all build install uninstall test vet fmt tidy clean run completions completion-zsh completion-bash completion-fish

all: build

build:
	$(GO) build -ldflags='$(LDFLAGS)' -o $(BINARY) .

install: build
	@mkdir -p $(BINDIR) $(CONFDIR)
	install -m 0755 $(BINARY) $(BINDIR)/$(BINARY)
	@if [ -e $(CONFDIR)/tmux.conf ]; then \
	  echo "kept existing $(CONFDIR)/tmux.conf"; \
	else \
	  install -m 0644 tmux.conf $(CONFDIR)/tmux.conf; \
	  echo "installed $(CONFDIR)/tmux.conf"; \
	fi
	@echo "installed $(BINDIR)/$(BINARY)"
	@case ":$$PATH:" in *":$(BINDIR):"*) ;; \
	  *) echo "note: $(BINDIR) is not in your PATH" ;; esac

uninstall:
	rm -f $(BINDIR)/$(BINARY)
	@echo "left $(CONFDIR) intact (remove manually if you want)"

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

clean:
	rm -f $(BINARY)

run: build
	./$(BINARY)

completions: completion-zsh completion-bash completion-fish

completion-zsh: build
	@mkdir -p $(ZSH_COMPDIR)
	./$(BINARY) completion zsh > $(ZSH_COMPDIR)/_$(BINARY)
	@echo "installed $(ZSH_COMPDIR)/_$(BINARY)"
	@echo "  add to ~/.zshrc if not already: fpath=($(ZSH_COMPDIR) \$$fpath); autoload -U compinit && compinit"

completion-bash: build
	@mkdir -p $(BASH_COMPDIR)
	./$(BINARY) completion bash > $(BASH_COMPDIR)/$(BINARY)
	@echo "installed $(BASH_COMPDIR)/$(BINARY)"

completion-fish: build
	@mkdir -p $(FISH_COMPDIR)
	./$(BINARY) completion fish > $(FISH_COMPDIR)/$(BINARY).fish
	@echo "installed $(FISH_COMPDIR)/$(BINARY).fish"
