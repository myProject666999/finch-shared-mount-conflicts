// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromConfigDirectories(t *testing.T) {
	t.Parallel()

	writable, readOnly := true, false
	dirs := []ConfigDirectory{
		{Path: "/a", Writable: &writable},
		{Path: "/b", Writable: &readOnly, MountPoint: "/mnt/b"},
		{Path: "/c"}, // nil Writable defaults to read-write for compatibility.
	}

	decls := FromConfigDirectories(dirs)
	require.Len(t, decls, 3)
	assert.Equal(t, 0, decls[0].Index)
	assert.Equal(t, ReadWrite, decls[0].Mode)
	assert.Equal(t, 1, decls[1].Index)
	assert.Equal(t, ReadOnly, decls[1].Mode)
	assert.Equal(t, "/mnt/b", decls[1].MountPoint)
	assert.Equal(t, ReadWrite, decls[2].Mode)
	for _, decl := range decls {
		assert.Equal(t, SourceNew, decl.Source)
	}
}

func TestToSnapshotEntriesRoundTrip(t *testing.T) {
	t.Parallel()

	decls := []Declaration{
		{Index: 0, HostPath: "/a", Mode: ReadOnly, MountPoint: "/mnt/a"},
		{Index: 1, HostPath: "/b", Mode: ReadWrite},
	}

	entries := ToSnapshotEntries(decls)
	require.Len(t, entries, 2)
	assert.False(t, entries[0].Writable)
	assert.Equal(t, "/mnt/a", entries[0].MountPoint)
	assert.True(t, entries[1].Writable)
	assert.Empty(t, entries[1].MountPoint)

	// The single-entry helper agrees with the slice helper.
	assert.Equal(t, entries[0], ToSnapshotEntry(decls[0]))
}

func TestFoldCaseAndSeparators(t *testing.T) {
	t.Parallel()

	assert.Equal(t, byte('/'), POSIXSemantics{CaseSensitive: true}.Separator())
	assert.Equal(t, byte('\\'), WindowsSemantics{}.Separator())
	assert.Equal(t, byte('/'), GuestSemantics{}.Separator())

	assert.Equal(t, "/AbC", POSIXSemantics{CaseSensitive: true}.FoldCase("/AbC"))
	assert.Equal(t, "/abc", POSIXSemantics{CaseSensitive: false}.FoldCase("/AbC"))
	assert.Equal(t, `c:\abc`, WindowsSemantics{}.FoldCase(`C:\AbC`))
	assert.Equal(t, "/AbC", GuestSemantics{}.FoldCase("/AbC"))
}

func TestStringRenderers(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "read-only", ReadOnly.String())
	assert.Equal(t, "read-write", ReadWrite.String())
	assert.Equal(t, "allow", VerdictAllow.String())
	assert.Equal(t, "deduplicate", VerdictDedup.String())
	assert.Equal(t, "warn", VerdictWarn.String())
	assert.Equal(t, "reject", VerdictReject.String())
	assert.Equal(t, "host path", KindHostPath.String())
	assert.Equal(t, "guest mount point", KindMountPoint.String())
	assert.Equal(t, "already in effect", SourceExisting.String())
	assert.Equal(t, "newly added", SourceNew.String())
	assert.Equal(t, "implicit default mount", SourceImplicit.String())
	assert.Equal(t, "same directory", SameEntity.String())
	assert.Equal(t, "nested directories (one contains the other)", AncestorDescendant.String())
	assert.Equal(t, "unrelated directories", Unrelated.String())
}

func TestReportHelpers(t *testing.T) {
	t.Parallel()

	report := &Report{Findings: []Finding{
		{Verdict: VerdictWarn},
		{Verdict: VerdictDedup},
		{Verdict: VerdictReject},
	}}
	assert.True(t, report.Rejects())
	require.Len(t, report.BlockingFindings(), 1)
	assert.Len(t, report.Warnings(), 2)

	empty := &Report{}
	assert.False(t, empty.Rejects())
	assert.Empty(t, empty.BlockingFindings())
}
