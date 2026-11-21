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

// checkerFixture builds a real on-disk tree used by the decision-table tests:
//
//	root/
//	  home/                  (shared read-only in some cases)
//	    project/             (shared read-write in the nesting case)
//	  other/
//	  alias -> home          (symlink, skipped on platforms without symlinks)
func checkerFixture(t *testing.T) (*Checker, string) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "home", "project"),
		filepath.Join(root, "other"),
		filepath.Join(root, "sibling"),
	} {
		require.NoError(t, os.MkdirAll(dir, 0o750))
	}
	if runtime.GOOS != "windows" {
		require.NoError(t, os.Symlink(filepath.Join(root, "home"), filepath.Join(root, "alias")))
	}

	sem := POSIXSemantics{CaseSensitive: true}
	resolver := NewResolver(afero.NewOsFs(), sem, filepath.Join(root, "home"))
	checker := NewChecker(afero.NewOsFs(), sem, GuestSemantics{}, resolver, ExistenceRejectStartup)
	return checker, root
}

func decl(index int, hostPath string, mode Mode, mountPoint string) Declaration {
	return Declaration{Index: index, HostPath: hostPath, Mode: mode, MountPoint: mountPoint, Source: SourceNew}
}

// firstPairVerdict returns the verdict for the (index 0, index 1) pair. All
// decision-table cases build exactly this two-entry pair.
func firstPairVerdict(findings []Finding) (Verdict, bool) {
	for _, f := range findings {
		if f.First.Index == 0 && f.Second.Index == 1 {
			return f.Verdict, true
		}
	}
	return VerdictAllow, false
}

func TestDecisionTable_SameEntity(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")

	t.Run("same path, both read-only: deduplicate", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, ""),
			decl(1, home, ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictDedup, verdict)
		assert.False(t, report.Rejects())
	})

	t.Run("same path, both read-write: deduplicate", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadWrite, ""),
			decl(1, home, ReadWrite, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictDedup, verdict)
	})

	t.Run("same path, read-only vs read-write: reject", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, ""),
			decl(1, home, ReadWrite, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictReject, verdict)
		assert.True(t, report.Rejects())
	})

	t.Run("order independence: read-write first, read-only second still rejects", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadWrite, ""),
			decl(1, home, ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictReject, verdict)
	})
}

func TestDecisionTable_NestedHostPaths(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")
	project := filepath.Join(root, "home", "project")

	t.Run("read-only ancestor, read-write descendant: warn (order independent)", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, ""),
			decl(1, project, ReadWrite, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictWarn, verdict)
		assert.False(t, report.Rejects())
	})

	t.Run("reversed declaration order gives the same verdict", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, project, ReadWrite, ""),
			decl(1, home, ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictWarn, verdict)
	})

	t.Run("read-write ancestor, read-only descendant: warn and allowed", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadWrite, ""),
			decl(1, project, ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictWarn, verdict)
		assert.False(t, report.Rejects())
	})

	t.Run("both read-only nesting: warn as redundant", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, ""),
			decl(1, project, ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictWarn, verdict)
	})

	t.Run("both read-write nesting: warn as redundant", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadWrite, ""),
			decl(1, project, ReadWrite, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictWarn, verdict)
	})
}

func TestDecisionTable_SameEntityDifferentSpellings(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")

	t.Run("trailing slash and dot segments collapse to the same entity", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home+"/", ReadOnly, ""),
			decl(1, home+"/./", ReadOnly, ""),
		})
		require.NoError(t, err)
		verdict, found := firstPairVerdict(report.Findings)
		require.True(t, found)
		assert.Equal(t, VerdictDedup, verdict)
	})

	if runtime.GOOS != "windows" {
		t.Run("symlink alias and real path resolve to the same entity", func(t *testing.T) {
			t.Parallel()
			report, err := checker.Check([]Declaration{
				decl(0, filepath.Join(root, "alias"), ReadOnly, ""),
				decl(1, home, ReadWrite, ""),
			})
			require.NoError(t, err)
			verdict, found := firstPairVerdict(report.Findings)
			require.True(t, found)
			assert.Equal(t, VerdictReject, verdict,
				"symlink and target with different modes must be a hard conflict")
		})
	}
}

func TestDecisionTable_CaseInsensitiveFileSystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	upper := filepath.Join(root, "Data")
	require.NoError(t, os.MkdirAll(upper, 0o750))

	sem := POSIXSemantics{CaseSensitive: false}
	resolver := NewResolver(afero.NewOsFs(), sem, root)
	checker := NewChecker(afero.NewOsFs(), sem, GuestSemantics{}, resolver, ExistenceRejectStartup)

	report, err := checker.Check([]Declaration{
		decl(0, upper, ReadOnly, ""),
		decl(1, filepath.Join(root, "data"), ReadWrite, ""),
	})
	require.NoError(t, err)
	verdict, found := firstPairVerdict(report.Findings)
	require.True(t, found)
	assert.Equal(t, VerdictReject, verdict)
}

func TestDecisionTable_MountPointConflicts(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")
	other := filepath.Join(root, "other")

	t.Run("different host paths, same guest mount point: reject", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, "/mnt/shared"),
			decl(1, other, ReadWrite, "/mnt/shared"),
		})
		require.NoError(t, err)

		var mountPointFinding *Finding
		for i := range report.Findings {
			if report.Findings[i].Kind == KindMountPoint {
				mountPointFinding = &report.Findings[i]
			}
		}
		require.NotNil(t, mountPointFinding)
		assert.Equal(t, VerdictReject, mountPointFinding.Verdict)
	})

	t.Run("different host and nested guest paths: allow", func(t *testing.T) {
		t.Parallel()
		report, err := checker.Check([]Declaration{
			decl(0, home, ReadOnly, "/mnt/shared"),
			decl(1, other, ReadWrite, "/mnt/shared/sub"),
		})
		require.NoError(t, err)
		for _, f := range report.Findings {
			assert.NotEqual(t, VerdictReject, f.Verdict)
		}
	})
}

func TestDecisionTable_UnrelatedPaths(t *testing.T) {
	t.Parallel()

	checker, root := checkerFixture(t)
	home := filepath.Join(root, "home")
	other := filepath.Join(root, "other")

	report, err := checker.Check([]Declaration{
		decl(0, home, ReadOnly, ""),
		decl(1, other, ReadWrite, ""),
	})
	require.NoError(t, err)
	assert.Empty(t, report.Findings)
	assert.False(t, report.Rejects())
}

func TestDecisionTable_ExistencePolicies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	existing := filepath.Join(root, "exists")
	require.NoError(t, os.MkdirAll(existing, 0o750))

	build := func(policy ExistencePolicy) *Checker {
		sem := POSIXSemantics{CaseSensitive: true}
		resolver := NewResolver(afero.NewOsFs(), sem, root)
		return NewChecker(afero.NewOsFs(), sem, GuestSemantics{}, resolver, policy)
	}

	t.Run("reject startup on missing directory", func(t *testing.T) {
		t.Parallel()
		missingHere := filepath.Join(root, "missing-reject")
		report, err := build(ExistenceRejectStartup).Check([]Declaration{decl(0, missingHere, ReadWrite, "")})
		require.NoError(t, err)
		require.NotEmpty(t, report.BlockingFindings())
		assert.Equal(t, VerdictReject, report.BlockingFindings()[0].Verdict)
	})

	t.Run("skip and warn on missing directory", func(t *testing.T) {
		t.Parallel()
		missingHere := filepath.Join(root, "missing-skip")
		report, err := build(ExistenceSkipWarn).Check([]Declaration{decl(0, missingHere, ReadWrite, "")})
		require.NoError(t, err)
		assert.Empty(t, report.BlockingFindings())
		require.Len(t, report.Warnings(), 1)
		assert.Equal(t, VerdictWarn, report.Warnings()[0].Verdict)
	})

	t.Run("auto-create the missing directory", func(t *testing.T) {
		t.Parallel()
		missingHere := filepath.Join(root, "missing-create")
		fs := afero.NewOsFs()
		sem := POSIXSemantics{CaseSensitive: true}
		resolver := NewResolver(fs, sem, root)
		checker := NewChecker(fs, sem, GuestSemantics{}, resolver, ExistenceAutoCreate)
		report, err := checker.Check([]Declaration{decl(0, missingHere, ReadWrite, "")})
		require.NoError(t, err)
		assert.False(t, report.Rejects())
		info, statErr := fs.Stat(missingHere)
		require.NoError(t, statErr)
		assert.True(t, info.IsDir())
	})

	t.Run("existing directory never triggers an existence finding", func(t *testing.T) {
		t.Parallel()
		report, err := build(ExistenceRejectStartup).Check([]Declaration{decl(0, existing, ReadWrite, "")})
		require.NoError(t, err)
		assert.Empty(t, report.Findings)
	})
}
