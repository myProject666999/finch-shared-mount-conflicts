// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func snapshotFixture(t *testing.T) (*Checker, *SnapshotStore, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"old", "new", "other"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o750))
	}
	sem := POSIXSemantics{CaseSensitive: true}
	resolver := NewResolver(afero.NewOsFs(), sem, root)
	checker := NewChecker(afero.NewOsFs(), sem, GuestSemantics{}, resolver, ExistenceRejectStartup)
	store := NewSnapshotStore(afero.NewOsFs(), filepath.Join(root, "snapshot.yaml"))
	return checker, store, root
}

func TestSnapshotStore_RoundTrip(t *testing.T) {
	t.Parallel()

	_, store, _ := snapshotFixture(t)
	entries := []SnapshotEntry{
		{HostPath: "/old", Writable: false, MountPoint: "/mnt/old"},
		{HostPath: "/new", Writable: true},
	}
	require.NoError(t, store.Save(entries))

	loaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, CurrentSnapshotVersion, loaded.Version)
	assert.Equal(t, entries, loaded.Entries)
}

func TestSnapshotStore_MissingFile(t *testing.T) {
	t.Parallel()

	_, store, _ := snapshotFixture(t)
	_, err := store.Load()
	assert.ErrorIs(t, err, ErrSnapshotMissing)

	assert.NoError(t, store.Clear())
}

func TestCheckAgainstSnapshot_NewEntryConflictsWithExisting(t *testing.T) {
	t.Parallel()

	checker, store, root := snapshotFixture(t)
	oldDir := filepath.Join(root, "old")

	// The previously active config mounted old read-only successfully.
	require.NoError(t, store.Save([]SnapshotEntry{{HostPath: oldDir, Writable: false}}))
	snapshot, err := store.Load()
	require.NoError(t, err)

	// The user now adds the same directory read-write.
	newDecls := []Declaration{decl(0, oldDir, ReadWrite, "")}
	report, notice := checker.CheckAgainstSnapshot(newDecls, snapshot)

	require.True(t, report.Rejects())
	finding := report.BlockingFindings()[0]
	assert.Equal(t, SourceExisting, finding.First.Source)
	assert.Equal(t, SourceNew, finding.Second.Source)
	// Both entries are item #1 within their own list: indices are preserved,
	// not shifted. Source labels and combined-slice order disambiguate them.
	assert.Equal(t, 0, finding.First.Index)
	assert.Equal(t, 0, finding.Second.Index)
	assert.Empty(t, notice)
}

func TestCheckAgainstSnapshot_MissingSnapshotTreatsEverythingAsNew(t *testing.T) {
	t.Parallel()

	checker, _, root := snapshotFixture(t)
	dir := filepath.Join(root, "old")

	report, notice := checker.CheckAgainstSnapshot(
		[]Declaration{decl(0, dir, ReadOnly, ""), decl(1, dir, ReadWrite, "")}, nil)

	require.True(t, report.Rejects())
	assert.Contains(t, notice, "treated as newly added")
	for _, f := range report.Findings {
		assert.Equal(t, SourceNew, f.First.Source)
		assert.Equal(t, SourceNew, f.Second.Source)
	}
}

func TestCheckAgainstSnapshot_UnrelatedNewEntryIsAllowed(t *testing.T) {
	t.Parallel()

	checker, store, root := snapshotFixture(t)
	oldDir := filepath.Join(root, "old")
	otherDir := filepath.Join(root, "other")

	require.NoError(t, store.Save([]SnapshotEntry{{HostPath: oldDir, Writable: true}}))
	snapshot, err := store.Load()
	require.NoError(t, err)

	report, _ := checker.CheckAgainstSnapshot(
		[]Declaration{decl(0, otherDir, ReadWrite, "")}, snapshot)
	assert.False(t, report.Rejects())
}

func TestCheckAgainstSnapshot_PreservesOriginalIndicesAndOrder(t *testing.T) {
	t.Parallel()

	checker, store, root := snapshotFixture(t)
	oldDir := filepath.Join(root, "old")
	newDir := filepath.Join(root, "new")

	require.NoError(t, store.Save([]SnapshotEntry{{HostPath: oldDir, Writable: false}}))
	snapshot, err := store.Load()
	require.NoError(t, err)

	// The new config has two entries; only the first conflicts with the
	// snapshot. The second's index must remain 1, i.e. its position in the
	// user's finch.yaml, not shifted by the snapshot length.
	report, _ := checker.CheckAgainstSnapshot([]Declaration{
		decl(0, oldDir, ReadWrite, ""),
		decl(1, newDir, ReadWrite, ""),
	}, snapshot)

	require.True(t, report.Rejects())
	finding := report.BlockingFindings()[0]
	assert.Equal(t, SourceExisting, finding.First.Source)
	assert.Equal(t, SourceNew, finding.Second.Source)
	assert.Equal(t, 0, finding.Second.Index, "new entry keeps its original item number")
}

func TestAllConflictsReportedTogetherAndInOriginalOrder(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")
	other := filepath.Join(root, "other")
	sibling := filepath.Join(root, "sibling")

	declarations := []Declaration{
		decl(0, home, ReadOnly, ""),
		decl(1, home, ReadWrite, ""), // hard conflict with #0
		decl(2, other, ReadOnly, "/same"),
		decl(3, sibling, ReadWrite, "/same"), // mount-point conflict with #2
	}
	report, err := checker.Check(declarations)
	require.NoError(t, err)

	rejecting := report.BlockingFindings()
	require.Len(t, rejecting, 2, "both independent conflicts must be reported in one pass")

	// Deterministic order follows original indices, never a sorted path order.
	assert.Equal(t, 0, rejecting[0].First.Index)
	assert.Equal(t, 1, rejecting[0].Second.Index)
	assert.Equal(t, 2, rejecting[1].First.Index)
	assert.Equal(t, 3, rejecting[1].Second.Index)

	// Re-running yields byte-identical ordering.
	report2, err := checker.Check(declarations)
	require.NoError(t, err)
	require.Equal(t, len(report.Findings), len(report2.Findings))
	for i := range report.Findings {
		assert.Equal(t, report.Findings[i].First.Index, report2.Findings[i].First.Index)
		assert.Equal(t, report.Findings[i].Second.Index, report2.Findings[i].Second.Index)
	}
}

func TestFindingMessagesNameEntriesAndRemediation(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")

	report, err := checker.Check([]Declaration{
		decl(0, home, ReadOnly, ""),
		decl(1, home, ReadWrite, ""),
	})
	require.NoError(t, err)
	require.True(t, report.Rejects())

	msg := report.BlockingFindings()[0].Message
	assert.Contains(t, msg, "#1")
	assert.Contains(t, msg, "#2")
	assert.Contains(t, msg, home)
	assert.NotEmpty(t, report.BlockingFindings()[0].Remediation)
}

func TestSymlinkLoopIsReportedAsCheckError(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink loops are covered on POSIX")
	}

	root := t.TempDir()
	loopA := filepath.Join(root, "loopA")
	loopB := filepath.Join(root, "loopB")
	require.NoError(t, os.Symlink(loopB, loopA))
	require.NoError(t, os.Symlink(loopA, loopB))

	sem := POSIXSemantics{CaseSensitive: true}
	resolver := NewResolver(afero.NewOsFs(), sem, root)
	checker := NewChecker(afero.NewOsFs(), sem, GuestSemantics{}, resolver, ExistenceRejectStartup)

	_, err := checker.Check([]Declaration{decl(0, loopA, ReadOnly, "")})
	require.Error(t, err)
}
