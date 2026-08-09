package domain

import (
	"errors"
	"fmt"
	"time"
)

const (
	// ProjectKindSingleRepo is the existing one-repository project shape.
	ProjectKindSingleRepo ProjectKind = "single_repo"
	// ProjectKindWorkspace is a parent root-as-repo plus child repositories.
	ProjectKindWorkspace ProjectKind = "workspace"
	// ProjectKindScratch is AO-managed plain-directory work for first-run use.
	ProjectKindScratch ProjectKind = "scratch"
	// RootWorkspaceRepoName is the reserved repo_name used for the parent root repo.
	RootWorkspaceRepoName = "__root__"
)

// ProjectKind describes how a registered project materialises session workspaces.
type ProjectKind string

// WithDefault returns ProjectKindSingleRepo when the stored value predates the kind column.
func (k ProjectKind) WithDefault() ProjectKind {
	if k == "" {
		return ProjectKindSingleRepo
	}
	return k
}

// ProjectRecord is the durable project registry row used by storage and services.
type ProjectRecord struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
	Kind          ProjectKind
	// Config holds the typed per-project configuration AO resolves at spawn. An
	// IsZero value means unset. ConfigReadError is non-empty only when the raw
	// durable JSON could not be decoded by this AO version. Callers may use the
	// remaining registry metadata for display, but must never treat Config as a
	// usable zero value or mutate the row while ConfigReadError is set.
	Config          ProjectConfig
	ConfigReadError string
}

// ErrProjectConfigUnreadable classifies a project-row mutation refused because
// its existing durable config cannot be decoded without losing information.
var ErrProjectConfigUnreadable = errors.New("project config unreadable")

// ErrProjectRoleMapConflict classifies a role-map compare-and-swap refusal.
// Callers must reload the current map rather than overwriting a newer edit.
var ErrProjectRoleMapConflict = errors.New("project role map changed")

// ProjectRoleMapConflictError carries the exact compare-and-swap values while
// remaining classifiable with ErrProjectRoleMapConflict.
type ProjectRoleMapConflictError struct {
	ProjectID      string
	ExpectedSHA256 string
	ActualSHA256   string
}

func (e *ProjectRoleMapConflictError) Error() string {
	return fmt.Sprintf("project %s: %s", e.ProjectID, ErrProjectRoleMapConflict)
}

func (e *ProjectRoleMapConflictError) Unwrap() error {
	return ErrProjectRoleMapConflict
}

// ProjectConfigUnreadableError identifies the fenced project without exposing
// its raw config bytes or decoder details through product surfaces.
type ProjectConfigUnreadableError struct {
	ProjectID string
	Cause     error
}

func (e *ProjectConfigUnreadableError) Error() string {
	if e == nil {
		return ErrProjectConfigUnreadable.Error()
	}
	if e.Cause == nil {
		return fmt.Sprintf("project %s: %s", e.ProjectID, ErrProjectConfigUnreadable)
	}
	return fmt.Sprintf("project %s: %s: %v", e.ProjectID, ErrProjectConfigUnreadable, e.Cause)
}

func (e *ProjectConfigUnreadableError) Unwrap() error {
	return ErrProjectConfigUnreadable
}

// WorkspaceRepoRecord is a child repo registered under a workspace project.
// The root repo itself is represented by ProjectRecord and by session_worktrees
// rows using RootWorkspaceRepoName; workspace_repos contains direct children.
type WorkspaceRepoRecord struct {
	ProjectID     ProjectID
	Name          string
	RelativePath  string
	RepoOriginURL string
	RegisteredAt  time.Time
}

// SessionWorktreeRecord tracks one repo worktree in a session. Workspace
// projects create one root row plus one child row per WorkspaceRepoRecord.
type SessionWorktreeRecord struct {
	SessionID    SessionID
	RepoName     string
	Branch       string
	BaseSHA      string
	WorktreePath string
	PreservedRef string
	// ponytail: State mirrors session_worktrees.state, an enum that is unused
	// multi-repo scaffolding. The save/restore lifecycle reads and writes only
	// PreservedRef and row presence; State is never set by any live code path
	// and always resolves to the column default ('active' on insert). Wire it
	// when multi-repo worktree lifecycle states actually ship.
	State string
}
