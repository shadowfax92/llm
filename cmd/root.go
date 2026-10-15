// Package cmd adapts Cobra flags/streams to contextstore and archive operations.
// Each command tree owns its state; embedders and tests never change process cwd.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dotllm/internal/archive"
	"dotllm/internal/config"
	"dotllm/internal/contextstore"
	"dotllm/internal/output"
	"github.com/spf13/cobra"
)

type Dependencies struct {
	In        io.Reader
	Out, Err  io.Writer
	Cwd, Home string
	IsTTY     bool
}
type app struct {
	deps                         Dependencies
	json, noInput                bool
	directory, storeFlag, color  string
	cwd, storeRoot, configSource string
	store                        *contextstore.Store
}

// operationError distinguishes an operation's I/O failure from Cobra parse or
// argument failures. Execute maps both to stable exit/error contracts.
type operationError struct{ error }

func (e operationError) Unwrap() error { return e.error }

func NewRoot(deps Dependencies) *cobra.Command {
	if deps.In == nil {
		deps.In = os.Stdin
	}
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.Err == nil {
		deps.Err = os.Stderr
	}
	a := &app{deps: deps}
	root := &cobra.Command{Use: "dotllm", Short: "Manage central .llm project context", Args: cobra.NoArgs, SilenceUsage: true, SilenceErrors: true}
	root.SetIn(deps.In)
	root.SetOut(deps.Out)
	root.SetErr(deps.Err)
	root.PersistentFlags().BoolVar(&a.json, "json", false, "Emit one versioned JSON result")
	root.PersistentFlags().BoolVar(&a.noInput, "no-input", false, "Disable interactive input")
	root.PersistentFlags().StringVarP(&a.directory, "directory", "C", "", "Run relative to this directory")
	root.PersistentFlags().StringVar(&a.storeFlag, "store", "", "Override the central context store")
	root.PersistentFlags().StringVar(&a.color, "color", "auto", "Color: auto, never, always")
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error { return a.prepare() }
	root.RunE = func(cmd *cobra.Command, args []string) error {
		result, err := a.store.Ensure(cmd.Context(), a.cwd, contextstore.ResolveOptions{Scope: contextstore.ScopeRepo})
		if err != nil {
			return operationError{err}
		}
		return a.write(cmd, "path", output.PathData{Path: result.Binding.Project.Root, Anchor: result.Binding.Anchor, Changed: result.Changed}, result.Warnings)
	}
	root.AddCommand(newInitCommand(a), newLinkCommand(a), newListCommand(a), newStatusCommand(a), newPathCommand(a), newConfigCommand(a), newArchiveCommand(a), newRestoreCommand(a))
	configureHelp(root)
	return root
}
func (a *app) prepare() error {
	if a.color != "auto" && a.color != "never" && a.color != "always" {
		return &contextstore.Error{Code: "invalid_input", Message: "--color must be auto, never or always", NextAction: "Use --color never for plain output."}
	}
	home := a.deps.Home
	var err error
	if home == "" {
		home, err = os.UserHomeDir()
		if err != nil {
			return operationError{err}
		}
	}
	a.cwd = a.deps.Cwd
	if a.cwd == "" {
		a.cwd, err = os.Getwd()
		if err != nil {
			return operationError{err}
		}
	}
	if a.directory != "" {
		if filepath.IsAbs(a.directory) {
			a.cwd = a.directory
		} else {
			a.cwd = filepath.Join(a.cwd, a.directory)
		}
	}
	cfg, err := config.Load(home)
	if err != nil {
		return &contextstore.Error{Code: "invalid_config", Message: err.Error(), NextAction: "Correct the versioned dotllm config file."}
	}
	a.configSource = "default"
	if _, err := os.Stat(config.Path(home)); err == nil {
		a.configSource = "config"
	}
	if value := os.Getenv("DOTLLM_ROOT"); value != "" {
		cfg.StoreRoot = value
		a.configSource = "DOTLLM_ROOT"
	}
	if a.storeFlag != "" {
		cfg.StoreRoot = a.storeFlag
		a.configSource = "--store"
	}
	a.storeRoot = cfg.StoreRoot
	a.deps.Home = home
	a.store, err = contextstore.Open(contextstore.Config{Root: cfg.StoreRoot, Home: home})
	return err
}
func (a *app) write(cmd *cobra.Command, name string, data any, warnings []string) error {
	value := output.Envelope{Version: 1, Command: name, Data: data, Warnings: warnings}
	if a.json {
		return operationResult(output.WriteJSON(cmd.OutOrStdout(), value))
	}
	for _, warning := range warnings {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), warning); err != nil {
			return operationError{err}
		}
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	color := a.color == "always" || (a.color == "auto" && a.deps.IsTTY && !noColor && os.Getenv("TERM") != "dumb")
	return operationResult(output.WriteText(cmd.OutOrStdout(), value, output.Options{Color: color, TerminalWidth: 80}))
}
func operationResult(err error) error {
	if err != nil {
		return operationError{err}
	}
	return nil
}

// Execute is the process adapter: data stays on stdout and one actionable error
// goes to stderr. NewRoot callers may use ExecuteContext for cancellation.
func Execute(args []string, deps Dependencies) int {
	root := NewRoot(deps)
	root.SetArgs(args)
	executed, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	detail := output.ErrorInfo{Code: "invalid_input", Message: err.Error(), NextAction: "Run dotllm --help for command syntax."}
	code := 2
	var op operationError
	if errors.As(err, &op) {
		detail.Code = "io_error"
		detail.NextAction = "Inspect the reported paths and retry."
		code = 5
	}
	var domain *contextstore.Error
	if errors.As(err, &domain) {
		detail = output.ErrorInfo{Code: domain.Code, Message: domain.Message, NextAction: domain.NextAction, Retryable: domain.Retryable}
		switch domain.Code {
		case "invalid_input", "invalid_config", "selector_required":
			code = 2
		case "missing_context", "missing_project":
			code = 3
		case "conflict", "stale_plan":
			code = 4
		default:
			code = 5
		}
	}
	if errors.Is(err, context.Canceled) {
		detail.Code = "cancelled"
		detail.NextAction = "Retry the cancelled command."
		code = 130
	}
	// Preserve the archive module's actual progress after error classification.
	// Rendering remains owned by output, including retained destination paths.
	// Keep the executed command so restore failures retain their own action name.
	var partial interface{ PartialResult() *archive.Result }
	if errors.As(err, &partial) {
		detail.Partial = partial.PartialResult()
	}
	jsonMode, _ := root.PersistentFlags().GetBool("json")
	// Cobra can reject a flag before parsing --json later in argv. Recognize that
	// exact persistent flag without ever treating an argument after -- as a flag.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			jsonMode = true
		}
		if arg == "--json=false" {
			jsonMode = false
		}
	}
	if jsonMode {
		_ = output.WriteErrorJSON(root.ErrOrStderr(), output.ErrorEnvelope{Version: 1, Error: detail})
	} else {
		fmt.Fprintln(root.ErrOrStderr(), strings.TrimSpace(detail.Message))
		fmt.Fprintln(root.ErrOrStderr(), detail.NextAction)
		if detail.Partial != nil {
			_ = output.WriteText(root.ErrOrStderr(), output.Envelope{Version: 1, Command: executed.Name(), Data: output.ApplyData{Result: *detail.Partial}}, output.Options{})
		}
	}
	return code
}
