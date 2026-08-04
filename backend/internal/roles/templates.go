// Package roles resolves multi-sub role maps, loads Intent-style templates,
// and pins immutable template artifacts for restore.
package roles

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// ArtifactStore holds immutable template bytes keyed by sha256.
type ArtifactStore struct {
	mu   sync.RWMutex
	byID map[string][]byte // id == "sha256:"+hex
}

// NewArtifactStore returns an in-process CAS. Durable disk CAS can wrap this later.
func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{byID: make(map[string][]byte)}
}

// Put stores raw bytes and returns artifact id "sha256:<hex>" and hex digest.
func (s *ArtifactStore) Put(raw []byte) (id, sha string) {
	sum := sha256.Sum256(raw)
	sha = hex.EncodeToString(sum[:])
	id = "sha256:" + sha
	s.mu.Lock()
	s.byID[id] = append([]byte(nil), raw...)
	s.mu.Unlock()
	return id, sha
}

// Get returns stored bytes by artifact id.
func (s *ArtifactStore) Get(id string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	out := append([]byte(nil), b...)
	return out, true
}

// Template is a parsed profile markdown file.
type Template struct {
	ID           string
	Name         string
	Description  string
	RoleReminder string
	Body         string
	Raw          []byte
	SHA256       string
	ArtifactID   string
}

type frontmatter struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Description  string   `yaml:"description"`
	RoleReminder string   `yaml:"roleReminder"`
	DefaultHarness string `yaml:"defaultHarness"`
	DefaultModel string   `yaml:"defaultModel"`
	When         []string `yaml:"when"`
}

// LoadTemplateFile reads a Markdown file with optional YAML frontmatter.
func LoadTemplateFile(path string, store *ArtifactStore) (Template, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is controlled by profile roots
	if err != nil {
		return Template{}, err
	}
	return ParseTemplate(raw, store)
}

// ParseTemplate parses raw markdown (+ optional frontmatter) and pins an artifact.
func ParseTemplate(raw []byte, store *ArtifactStore) (Template, error) {
	id, sha := "", ""
	if store != nil {
		id, sha = store.Put(raw)
	} else {
		sum := sha256.Sum256(raw)
		sha = hex.EncodeToString(sum[:])
		id = "sha256:" + sha
	}
	text := string(raw)
	var fm frontmatter
	body := text
	if strings.HasPrefix(text, "---\n") || strings.HasPrefix(text, "---\r\n") {
		rest := text[3:]
		if strings.HasPrefix(rest, "\r\n") {
			rest = rest[2:]
		} else if strings.HasPrefix(rest, "\n") {
			rest = rest[1:]
		}
		end := strings.Index(rest, "\n---")
		if end >= 0 {
			yamlBlock := rest[:end]
			body = strings.TrimPrefix(rest[end+4:], "\n")
			body = strings.TrimPrefix(body, "\r\n")
			if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
				return Template{}, fmt.Errorf("frontmatter: %w", err)
			}
		}
	}
	t := Template{
		ID:           strings.TrimSpace(fm.ID),
		Name:         strings.TrimSpace(fm.Name),
		Description:  strings.TrimSpace(fm.Description),
		RoleReminder: strings.TrimSpace(fm.RoleReminder),
		Body:         strings.TrimSpace(body),
		Raw:          append([]byte(nil), raw...),
		SHA256:       sha,
		ArtifactID:   id,
	}
	return t, nil
}

// SystemPrompt renders the worker/orchestrator system section from a template.
func (t Template) SystemPrompt() string {
	var b strings.Builder
	if t.Name != "" {
		fmt.Fprintf(&b, "# Role: %s\n\n", t.Name)
	}
	if t.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", t.Description)
	}
	if t.Body != "" {
		b.WriteString(t.Body)
		b.WriteString("\n")
	}
	if t.RoleReminder != "" {
		fmt.Fprintf(&b, "\n## Role reminder (follow)\n%s\n", t.RoleReminder)
	}
	return strings.TrimSpace(b.String())
}

// Loader loads templates from an ordered list of root directories
// (first match wins). Typical order: shipped profiles/, then approved base only.
type Loader struct {
	Roots  []string
	Store  *ArtifactStore
	cache  map[string]Template
	mu     sync.Mutex
}

// NewLoader constructs a template loader.
func NewLoader(store *ArtifactStore, roots ...string) *Loader {
	if store == nil {
		store = NewArtifactStore()
	}
	return &Loader{Roots: roots, Store: store, cache: make(map[string]Template)}
}

// Load returns a template by id (filename without .md).
func (l *Loader) Load(templateID string) (Template, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return Template{}, fmt.Errorf("template id: empty")
	}
	if strings.Contains(templateID, "..") || strings.ContainsAny(templateID, `/\`) {
		return Template{}, fmt.Errorf("template id %q: path separators forbidden", templateID)
	}
	l.mu.Lock()
	if t, ok := l.cache[templateID]; ok {
		l.mu.Unlock()
		return t, nil
	}
	l.mu.Unlock()

	var lastErr error
	for _, root := range l.Roots {
		if root == "" {
			continue
		}
		path := filepath.Join(root, templateID+".md")
		// Containment: resolved path must stay under root.
		absRoot, err := filepath.Abs(root)
		if err != nil {
			lastErr = err
			continue
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			lastErr = err
			continue
		}
		if !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) && absPath != absRoot {
			return Template{}, fmt.Errorf("template %q: escapes root", templateID)
		}
		t, err := LoadTemplateFile(absPath, l.Store)
		if err != nil {
			lastErr = err
			continue
		}
		if t.ID == "" {
			t.ID = templateID
		}
		l.mu.Lock()
		l.cache[templateID] = t
		l.mu.Unlock()
		return t, nil
	}
	if lastErr != nil {
		return Template{}, fmt.Errorf("template %q: %w", templateID, lastErr)
	}
	return Template{}, fmt.Errorf("template %q: not found in roots %v", templateID, l.Roots)
}
