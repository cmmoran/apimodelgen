package manifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Snapshot represents a generated API snapshot entry in the manifest.
type Snapshot struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	File    string `yaml:"file" json:"file"`
}

// Manifest tracks the lifecycle of generated API snapshots.
type Manifest struct {
	CurrentVersion  string     `yaml:"current_version" json:"current_version"`
	PreviousVersion string     `yaml:"previous_version" json:"previous_version"`
	Snapshots       []Snapshot `yaml:"snapshots" json:"snapshots"`
}

// Load reads a manifest from the provided path. If the file does not exist,
// an empty manifest is returned.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Manifest{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	return &m, nil
}

// Save writes the manifest to the provided path, creating parent directories as needed.
func (m *Manifest) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	return nil
}

// AddSnapshot records a snapshot, updating version pointers and de-duplicating
// existing entries that share the same name and version.
func (m *Manifest) AddSnapshot(s Snapshot) {
	if m.CurrentVersion != "" {
		m.PreviousVersion = m.CurrentVersion
	}
	m.CurrentVersion = s.Version

	for i := range m.Snapshots {
		if m.Snapshots[i].Name == s.Name && m.Snapshots[i].Version == s.Version {
			m.Snapshots[i] = s
			return
		}
	}

	m.Snapshots = append(m.Snapshots, s)
}

// SnapshotFile returns the path associated with the provided version, if present.
func (m *Manifest) SnapshotFile(version string) string {
	for _, s := range m.Snapshots {
		if s.Version == version {
			return s.File
		}
	}
	return ""
}
