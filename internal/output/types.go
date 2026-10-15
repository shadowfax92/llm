// Package output owns presentation of command results. Typed command data keeps
// the human renderer independent of command, Git and filesystem implementations.
package output

import (
	"dotllm/internal/archive"
	"dotllm/internal/contextstore"
)

type Envelope struct {
	Version  int      `json:"version"`
	Command  string   `json:"command"`
	Data     any      `json:"data"`
	Warnings []string `json:"warnings"`
}
type Options struct {
	Color         bool
	TerminalWidth int
}
type PathData struct {
	Path    string `json:"path"`
	Anchor  string `json:"anchor"`
	Changed bool   `json:"changed"`
}
type StatusData struct {
	Binding contextstore.Binding `json:"binding"`
}
type LinkData = contextstore.LinkResult
type ListData struct {
	Projects []contextstore.Project `json:"projects"`
	Current  string                 `json:"current"`
}
type ConfigData struct {
	Path      string               `json:"path"`
	StoreRoot string               `json:"store_root"`
	Source    string               `json:"source"`
	Binding   contextstore.Binding `json:"binding"`
}
type ArchiveData struct {
	Plan archive.Plan `json:"plan"`
}
type ApplyData struct {
	Result archive.Result `json:"result"`
}
type ErrorInfo struct {
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	NextAction string          `json:"next_action"`
	Retryable  bool            `json:"retryable"`
	Partial    *archive.Result `json:"partial,omitempty"`
}
type ErrorEnvelope struct {
	Version int       `json:"version"`
	Error   ErrorInfo `json:"error"`
}
