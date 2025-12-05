package snapshot

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/go-cmp/cmp"

	"github.com/cmmoran/apimodelgen/pkg/action/initialize"
	"github.com/cmmoran/apimodelgen/pkg/manifest"
	"github.com/cmmoran/apimodelgen/pkg/parser"
)

// Generate writes a snapshot of the current API definitions and records it in the manifest.
func Generate(opts *parser.Options, manifestPath, snapshotName, snapshotVersion string) (string, error) {
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return "", err
	}

	initialize.Generate(opts)

	outFile := filepath.Clean(filepath.Join(opts.OutDir, opts.OutFile))
	m.AddSnapshot(manifest.Snapshot{Name: snapshotName, Version: snapshotVersion, File: outFile})

	if err := m.Save(manifestPath); err != nil {
		return "", err
	}

	return outFile, nil
}

// List returns all snapshots recorded in the manifest.
func List(manifestPath string) (*manifest.Manifest, error) {
	return manifest.Load(manifestPath)
}

// DiffCurrentWithPrevious loads the manifest, locates the current and previous
// snapshot files, and returns a textual diff of their contents.
func DiffCurrentWithPrevious(manifestPath string) (string, error) {
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return "", err
	}

	if m.CurrentVersion == "" || m.PreviousVersion == "" {
		return "", fmt.Errorf("no current/previous snapshots recorded")
	}

	currentPath := m.SnapshotFile(m.CurrentVersion)
	previousPath := m.SnapshotFile(m.PreviousVersion)

	if currentPath == "" || previousPath == "" {
		return "", fmt.Errorf("snapshot files not found in manifest")
	}

	current, err := os.ReadFile(currentPath)
	if err != nil {
		return "", fmt.Errorf("read current snapshot: %w", err)
	}

	previous, err := os.ReadFile(previousPath)
	if err != nil {
		return "", fmt.Errorf("read previous snapshot: %w", err)
	}

	return cmp.Diff(string(previous), string(current)), nil
}
