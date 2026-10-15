package archive

import (
	"context"
	"dotllm/internal/contextstore"
)

// Manager's implementation owns filesystem/journal ordering, preflight and
// cancellation. Contextstore remains the single owner of project identity.
type Manager struct{ store *contextstore.Store }

func New(store *contextstore.Store) *Manager { return &Manager{store: store} }
func (m *Manager) Plan(ctx context.Context, selection Selection) (Plan, error) {
	return Plan{}, notReady(ctx)
}
func (m *Manager) PlanRestore(ctx context.Context, archiveID string, entries []string) (Plan, error) {
	return Plan{}, notReady(ctx)
}
func (m *Manager) Apply(ctx context.Context, plan Plan) (Result, error) {
	return Result{}, notReady(ctx)
}
func (m *Manager) Recover(ctx context.Context, archiveID string) (Result, error) {
	return Result{}, notReady(ctx)
}

// Unfinished lifecycle operations must not emit an empty successful plan or
// move files: the next owner must provide fingerprint preflight and journals.
func notReady(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &contextstore.Error{Code: "unavailable", Message: "reviewed archive/restore is unavailable in the skeleton", NextAction: "Wait for the archive implementation slice; no files were moved."}
}
