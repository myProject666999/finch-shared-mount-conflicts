// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"errors"
	"fmt"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"
)

// SnapshotEntry is one shared-directory declaration recorded after the last
// successful VM start. It stores raw paths so the next check can resolve them
// against the current file system rather than freezing stale symlink targets.
type SnapshotEntry struct {
	HostPath   string `yaml:"hostPath"`
	Writable   bool   `yaml:"writable"`
	MountPoint string `yaml:"mountPoint,omitempty"`
}

// Snapshot is the on-disk record of the declarations that were in effect when
// the VM last started successfully.
type Snapshot struct {
	// Version allows future schema changes.
	Version int             `yaml:"version"`
	Entries []SnapshotEntry `yaml:"entries"`
}

// CurrentSnapshotVersion is bumped whenever Snapshot's layout changes.
const CurrentSnapshotVersion = 1

// ErrSnapshotMissing is returned by Load when no snapshot has been recorded
// yet. Callers treat this as "every declaration is new" and must surface the
// resulting false-positive risk in their message.
var ErrSnapshotMissing = errors.New("no shared-directory snapshot from a previous successful VM start")

// SnapshotStore persists the last-known-good shared-directory declarations.
type SnapshotStore struct {
	fs   afero.Fs
	path string
}

// NewSnapshotStore builds a store reading and writing path inside fs.
func NewSnapshotStore(fs afero.Fs, path string) *SnapshotStore {
	return &SnapshotStore{fs: fs, path: path}
}

// Load reads the snapshot. It returns ErrSnapshotMissing when the file does
// not exist.
func (s *SnapshotStore) Load() (*Snapshot, error) {
	bytes, err := afero.ReadFile(s.fs, s.path)
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			return nil, ErrSnapshotMissing
		}
		return nil, fmt.Errorf("failed to read shared-directory snapshot: %w", err)
	}

	var snapshot Snapshot
	if err := yaml.Unmarshal(bytes, &snapshot); err != nil {
		return nil, fmt.Errorf("failed to parse shared-directory snapshot: %w", err)
	}
	return &snapshot, nil
}

// Save writes declarations as the new snapshot. It is called only after a
// successful start, so a failed check never overwrites the known-good record.
func (s *SnapshotStore) Save(entries []SnapshotEntry) error {
	snapshot := Snapshot{Version: CurrentSnapshotVersion, Entries: entries}
	bytes, err := yaml.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("failed to marshal shared-directory snapshot: %w", err)
	}
	if err := afero.WriteFile(s.fs, s.path, bytes, 0o600); err != nil {
		return fmt.Errorf("failed to write shared-directory snapshot: %w", err)
	}
	return nil
}

// Clear removes the snapshot, used when a VM is removed.
func (s *SnapshotStore) Clear() error {
	if err := s.fs.Remove(s.path); err != nil && !errors.Is(err, afero.ErrFileNotFound) {
		return fmt.Errorf("failed to remove shared-directory snapshot: %w", err)
	}
	return nil
}

// ToDeclarations converts a snapshot into Declaration values, marking every
// entry as SourceExisting. Indices are taken from the snapshot's stored order;
// callers offset new declarations after them so all reported indices are
// unique.
func (snapshot *Snapshot) ToDeclarations() []Declaration {
	declarations := make([]Declaration, 0, len(snapshot.Entries))
	for i, entry := range snapshot.Entries {
		mode := ReadOnly
		if entry.Writable {
			mode = ReadWrite
		}
		declarations = append(declarations, Declaration{
			Index:      i,
			HostPath:   entry.HostPath,
			Mode:       mode,
			MountPoint: entry.MountPoint,
			Source:     SourceExisting,
		})
	}
	return declarations
}

// CheckAgainstSnapshot combines the current (new) declarations with the last
// snapshot and runs the full decision table.
//
// Snapshot entries come first, followed by the new declarations. Each side
// keeps its own original Index, so an item number in a message is always the
// entry's position within its own configuration list (and therefore matches
// the user's finch.yaml for new entries). Findings are ordered by combined
// slice position; the Source field disambiguates two entries that share an
// index.
//
// When no snapshot exists every declaration is treated as new; the returned
// notice explains that conflicts against previously working shares cannot be
// distinguished and may therefore be over-reported.
func (c *Checker) CheckAgainstSnapshot(newDecls []Declaration, snapshot *Snapshot) (*Report, string) {
	var all []Declaration
	notice := ""
	if snapshot == nil {
		all = append(all, newDecls...)
		notice = "no snapshot of previously active shared directories was found; " +
			"every entry is treated as newly added, so conflicts that an older configuration " +
			"would have already allowed can be reported as new errors"
	} else {
		all = append(all, snapshot.ToDeclarations()...)
		// Original indices are intentionally preserved; ordering is carried by
		// slice position, not by Index.
		all = append(all, newDecls...)
	}

	report, err := c.Check(all)
	if err != nil {
		// Check only returns an error for unresolvable symlink loops; wrap it
		// with the same shape callers expect from a plain Check.
		return &Report{Findings: []Finding{{
			Kind:        KindHostPath,
			Verdict:     VerdictReject,
			Message:     err.Error(),
			Remediation: "fix or remove the symbolic link referenced by the entry",
		}}}, notice
	}
	return report, notice
}
