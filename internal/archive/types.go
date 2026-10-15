// Package archive owns reviewed plans, journaled moves and restoration. The
// skeleton freezes this seam but does not expose legacy immediate moves.
package archive

import (
	"dotllm/internal/contextstore"
	"time"
)

type Selection struct {
	Projects  []contextstore.Project `json:"projects"`
	OlderThan time.Time              `json:"older_than"`
	Entries   []string               `json:"entries"`
}
type Entry struct {
	ProjectKey    string    `json:"project_key"`
	RelativePath  string    `json:"relative_path"`
	Fingerprint   string    `json:"fingerprint"`
	SizeBytes     int64     `json:"size_bytes"`
	NewestModTime time.Time `json:"newest_mod_time"`
}
type Plan struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	Cutoff    time.Time `json:"cutoff"`
	Entries   []Entry   `json:"entries"`
	Warnings  []string  `json:"warnings"`
}
type Outcome struct {
	ProjectKey   string `json:"project_key"`
	RelativePath string `json:"relative_path"`
	Destination  string `json:"destination"`
	State        string `json:"state"`
	Error        string `json:"error"`
}
type Result struct {
	PlanID   string    `json:"plan_id"`
	Outcomes []Outcome `json:"outcomes"`
	Complete bool      `json:"complete"`
}
