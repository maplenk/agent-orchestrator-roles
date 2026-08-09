package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertProject inserts or replaces a registered project row.
func (s *Store) UpsertProject(ctx context.Context, r domain.ProjectRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "upsert project", func(q *gen.Queries) error {
		if err := ensureProjectConfigReadable(ctx, q, r.ID); err != nil {
			return err
		}
		return upsertProject(ctx, q, r, config)
	})
}

// UpsertWorkspaceProject inserts or replaces a workspace project and its child
// repository registry in one transaction. The child set is authoritative.
func (s *Store) UpsertWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	return s.writeWorkspaceProject(ctx, "upsert workspace project", r, repos, func(q *gen.Queries) error {
		return upsertProject(ctx, q, r, config)
	})
}

// ImportWorkspaceProject inserts or replaces a workspace project from an
// authoritative import source, including the source registration timestamp.
func (s *Store) ImportWorkspaceProject(ctx context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	config, err := marshalProjectConfig(r.Config)
	if err != nil {
		return err
	}
	return s.writeWorkspaceProject(ctx, "import workspace project", r, repos, func(q *gen.Queries) error {
		return importProject(ctx, q, r, config)
	})
}

func (s *Store) writeWorkspaceProject(ctx context.Context, label string, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord, writeProject func(*gen.Queries) error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, label, func(q *gen.Queries) error {
		if err := ensureProjectConfigReadable(ctx, q, r.ID); err != nil {
			return err
		}
		if err := writeProject(q); err != nil {
			return err
		}
		if err := q.DeleteWorkspaceReposByProject(ctx, domain.ProjectID(r.ID)); err != nil {
			return err
		}
		for _, repo := range repos {
			if err := q.UpsertWorkspaceRepo(ctx, gen.UpsertWorkspaceRepoParams{
				ProjectID:     domain.ProjectID(r.ID),
				Name:          repo.Name,
				RelativePath:  repo.RelativePath,
				RepoOriginURL: repo.RepoOriginURL,
				RegisteredAt:  repo.RegisteredAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListWorkspaceRepos returns the registered direct child repos for a workspace project.
func (s *Store) ListWorkspaceRepos(ctx context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	rows, err := s.qr.ListWorkspaceRepos(ctx, domain.ProjectID(projectID))
	if err != nil {
		return nil, fmt.Errorf("list workspace repos for %s: %w", projectID, err)
	}
	out := make([]domain.WorkspaceRepoRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.WorkspaceRepoRecord{
			ProjectID:     row.ProjectID,
			Name:          row.Name,
			RelativePath:  row.RelativePath,
			RepoOriginURL: row.RepoOriginURL,
			RegisteredAt:  row.RegisteredAt,
		})
	}
	return out, nil
}

func upsertProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	kind := r.Kind.WithDefault()
	return q.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

func importProject(ctx context.Context, q *gen.Queries, r domain.ProjectRecord, config sql.NullString) error {
	existingByID, err := q.GetProject(ctx, domain.ProjectID(r.ID))
	if err == nil {
		if existingByID.ArchivedAt.Valid {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDArchivedTarget,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
		if existingByID.Path != r.Path {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSameIDDifferentActivePath,
				TargetID:   string(existingByID.ID),
				TargetPath: existingByID.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project id conflict: %w", err)
	}

	existingByPath, err := q.FindProjectByPath(ctx, r.Path)
	if err == nil {
		if existingByPath.ID != domain.ProjectID(r.ID) {
			return &domain.ProjectImportConflictError{Conflict: domain.ProjectImportConflict{
				ProjectID:  r.ID,
				Path:       r.Path,
				Reason:     domain.ProjectImportConflictSamePathDifferentActiveID,
				TargetID:   string(existingByPath.ID),
				TargetPath: existingByPath.Path,
			}}
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check imported project path conflict: %w", err)
	}

	kind := r.Kind.WithDefault()
	return q.UpsertImportedProject(ctx, gen.UpsertImportedProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		Config:        config,
		Kind:          string(kind),
	})
}

// GetProject returns a project by id, active or archived.
func (s *Store) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	r, err := projectRowFromGen(p)
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	return r, true, nil
}

// GetProjectEntry returns registry metadata even when this AO version cannot
// decode the stored config. ConfigReadError distinguishes that degraded row;
// Config must not be consumed in that state.
func (s *Store) GetProjectEntry(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project entry %s: %w", id, err)
	}
	return projectEntryFromGen(p), true, nil
}

// FindProjectByPath returns a project registered at path, active or archived.
func (s *Store) FindProjectByPath(ctx context.Context, path string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.FindProjectByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	r, err := projectRowFromGen(p)
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	return r, true, nil
}

// ListProjects returns active projects ordered by id. A config decode failure
// is contained to that row via ConfigReadError so unrelated projects remain
// listable. Consumers that need Config must explicitly reject or skip such rows.
func (s *Store) ListProjects(ctx context.Context) ([]domain.ProjectRecord, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.ProjectRecord, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectEntryFromGen(p))
	}
	return out, nil
}

// UpdateProjectSettings atomically updates the user-facing display name and
// config for an active project. It returns ok=false when the project is missing
// or archived.
func (s *Store) UpdateProjectSettings(ctx context.Context, id, displayName string, config domain.ProjectConfig) (bool, error) {
	encodedConfig, err := marshalProjectConfig(config)
	if err != nil {
		return false, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var rows int64
	err = s.inTx(ctx, "update project settings", func(q *gen.Queries) error {
		if err := ensureProjectConfigReadable(ctx, q, id); err != nil {
			return err
		}
		var err error
		rows, err = q.UpdateProjectSettings(ctx, gen.UpdateProjectSettingsParams{
			ID:          domain.ProjectID(id),
			DisplayName: displayName,
			Config:      encodedConfig,
		})
		return err
	})
	if err != nil {
		return false, fmt.Errorf("update project settings %s: %w", id, err)
	}
	return rows > 0, nil
}

// CountProjectsIncludingArchived returns all registry rows, including projects
// the user archived. It is intentionally separate from ListProjects so first-run
// seeding does not recreate Scratch after any project has existed.
func (s *Store) CountProjectsIncludingArchived(ctx context.Context) (int, error) {
	count, err := s.qr.CountProjectsIncludingArchived(ctx)
	if err != nil {
		return 0, fmt.Errorf("count projects including archived: %w", err)
	}
	return int(count), nil
}

// ArchiveProject soft-deletes a project and reports whether a row was affected.
func (s *Store) ArchiveProject(ctx context.Context, id string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var n int64
	err := s.inTx(ctx, "archive project", func(q *gen.Queries) error {
		if err := ensureProjectConfigReadable(ctx, q, id); err != nil {
			return err
		}
		var err error
		n, err = q.ArchiveProject(ctx, gen.ArchiveProjectParams{
			ArchivedAt: nullTime(at),
			ID:         domain.ProjectID(id),
		})
		return err
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func projectRowFromGen(p gen.Project) (domain.ProjectRecord, error) {
	r := projectEntryFromGen(p)
	if r.ConfigReadError != "" {
		return domain.ProjectRecord{}, &domain.ProjectConfigUnreadableError{
			ProjectID: string(p.ID),
			Cause:     errors.New(r.ConfigReadError),
		}
	}
	return r, nil
}

func projectEntryFromGen(p gen.Project) domain.ProjectRecord {
	config, configErr := unmarshalProjectConfig(p.Config)
	r := domain.ProjectRecord{
		ID:            string(p.ID),
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginURL,
		DisplayName:   p.DisplayName,
		RegisteredAt:  p.RegisteredAt,
		Kind:          domain.ProjectKind(p.Kind).WithDefault(),
		Config:        config,
	}
	if configErr != nil {
		r.ConfigReadError = configErr.Error()
	}
	if p.ArchivedAt.Valid {
		r.ArchivedAt = p.ArchivedAt.Time
	}
	return r
}

// marshalProjectConfig encodes the typed per-project config into the nullable
// JSON column. An IsZero config stores SQL NULL so an unset config round-trips
// back to a zero value rather than an empty object.
func marshalProjectConfig(cfg domain.ProjectConfig) (sql.NullString, error) {
	if cfg.IsZero() {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal project config: %w", err)
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// unmarshalProjectConfig decodes the nullable JSON column back into the typed
// struct. SQL NULL (an unset config) decodes to a zero value. Invalid persisted
// JSON is an explicit read error: returning a partial or zero config would let
// an unrelated read-modify-write silently replace the authored value.
func unmarshalProjectConfig(s sql.NullString) (domain.ProjectConfig, error) {
	if !s.Valid {
		return domain.ProjectConfig{}, nil
	}
	if bytes.Equal(bytes.TrimSpace([]byte(s.String)), []byte("null")) {
		return domain.ProjectConfig{}, errors.New("unmarshal project config: JSON null is not a stored config")
	}
	var cfg domain.ProjectConfig
	dec := json.NewDecoder(bytes.NewReader([]byte(s.String)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return domain.ProjectConfig{}, fmt.Errorf("unmarshal project config: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return domain.ProjectConfig{}, fmt.Errorf("unmarshal project config: %w", err)
	}
	if version := cfg.RoleMap.SchemaVersion; version != 0 && version != domain.RoleMapSchemaVersion {
		return domain.ProjectConfig{}, fmt.Errorf("roleMap.role_map_schema_version: unsupported %d (want %d)", version, domain.RoleMapSchemaVersion)
	}
	return cfg, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("multiple JSON values")
}

func ensureProjectConfigReadable(ctx context.Context, q *gen.Queries, id string) error {
	p, err := q.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check project %s config before mutation: %w", id, err)
	}
	if _, err := unmarshalProjectConfig(p.Config); err != nil {
		return &domain.ProjectConfigUnreadableError{ProjectID: id, Cause: err}
	}
	return nil
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
