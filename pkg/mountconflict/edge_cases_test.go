// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowsSemantics_UNCEdgeCases(t *testing.T) {
	t.Parallel()

	sem := WindowsSemantics{}
	// A root-only UNC path without a share stays usable rather than panicking.
	assert.NotPanics(t, func() {
		cleaned := sem.Clean(`\\server`)
		assert.NotEmpty(t, cleaned)
	})

	// UNC roots and their sub-paths compare as ancestor/descendant regardless
	// of separator spelling.
	root := sem.Clean(`//server/share`)
	sub := sem.Clean(`\\server\share\sub\dir`)
	assert.True(t, sem.IsAncestor(root, sub))

	// A case-sensitive Windows volume does not fold case.
	sensitive := WindowsSemantics{CaseSensitive: true}
	assert.False(t, sensitive.Equal(`c:\A`, `c:\a`))
	assert.Equal(t, `c:\A`, sensitive.FoldCase(`c:\A`))
}

func TestSnapshotStore_CorruptAndClear(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	store := NewSnapshotStore(fs, "/lima/snap.yaml")

	// A missing snapshot reports the sentinel error.
	_, err := store.Load()
	assert.ErrorIs(t, err, ErrSnapshotMissing)

	// Corrupt YAML produces a parse error rather than panicking.
	require.NoError(t, afero.WriteFile(fs, "/lima/snap.yaml", []byte("version: [not-a-map"), 0o600))
	_, err = store.Load()
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrSnapshotMissing)

	// Clearing a missing file is not an error.
	store2 := NewSnapshotStore(fs, "/lima/missing.yaml")
	assert.NoError(t, store2.Clear())
}

func TestResolver_MemMapFsAndEmptyHome(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	// An empty home directory disables tilde expansion rather than rewriting.
	resolver := NewResolver(fs, POSIXSemantics{CaseSensitive: true}, "")
	identity, _, err := resolver.Resolve("~/project")
	require.NoError(t, err)
	assert.Equal(t, "~/project", identity)
}

func TestChecker_RelativePathIsLexicallyCompared(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	sem := POSIXSemantics{CaseSensitive: true}
	resolver := NewResolver(fs, sem, "")
	checker := NewChecker(fs, sem, GuestSemantics{}, resolver, ExistenceSkipWarn)

	// Relative paths are not absolute; they still compare deterministically and
	// do not crash the checker. The existence policy downgrades the missing
	// entries to warnings, so startup is not blocked.
	report, err := checker.Check([]Declaration{
		{Index: 0, HostPath: "rel/path", Mode: ReadOnly},
		{Index: 1, HostPath: "rel/path", Mode: ReadOnly},
	})
	require.NoError(t, err)
	// Same lexical spelling, same mode: a dedup finding is still produced.
	var hasDedup bool
	for _, f := range report.Findings {
		if f.Verdict == VerdictDedup {
			hasDedup = true
		}
	}
	assert.True(t, hasDedup)
}
