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

// newOSFSTestResolver builds a resolver backed by a real temporary directory so
// symlinks can be created.
func newOSFSTestResolver(t *testing.T) (*Resolver, string) {
	t.Helper()
	tmp := t.TempDir()
	sem := POSIXSemantics{CaseSensitive: true}
	return NewResolver(afero.NewOsFs(), sem, filepath.Join(tmp, "home")), tmp
}

func TestResolver_LexicalNormalizationWithRealDirs(t *testing.T) {
	t.Parallel()

	resolver, tmp := newOSFSTestResolver(t)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "home", "project"), 0o750))

	cases := map[string]string{
		filepath.Join(tmp, "home", "project"):                  filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home", "project") + "/":            filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home", "./project"):                filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home", "project", "..", "project"): filepath.Join(tmp, "home", "project"),
		filepath.Join(tmp, "home//project"):                    filepath.Join(tmp, "home", "project"),
	}
	for in, want := range cases {
		identity, exists, err := resolver.Resolve(in)
		require.NoError(t, err, "input %q", in)
		assert.True(t, exists, "input %q", in)
		assert.Equal(t, want, identity, "input %q", in)
	}
}

func TestResolver_SymlinkToRealDirectory(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode on Windows; covered on POSIX")
	}

	resolver, tmp := newOSFSTestResolver(t)
	realDir := filepath.Join(tmp, "real", "data")
	require.NoError(t, os.MkdirAll(realDir, 0o750))
	linkDir := filepath.Join(tmp, "links")
	require.NoError(t, os.MkdirAll(linkDir, 0o750))
	link := filepath.Join(linkDir, "alias")
	require.NoError(t, os.Symlink(realDir, link))

	identity, exists, err := resolver.Resolve(link)
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, realDir, identity)

	// The symlink alias and the real directory must resolve to the same entity.
	other, _, err := resolver.Resolve(realDir)
	require.NoError(t, err)
	assert.Equal(t, other, identity)
}

func TestResolver_RelativeSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode on Windows; covered on POSIX")
	}

	resolver, tmp := newOSFSTestResolver(t)
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "a", "b"), 0o750))
	require.NoError(t, os.Symlink("../a", filepath.Join(tmp, "a", "link")))

	identity, exists, err := resolver.Resolve(filepath.Join(tmp, "a", "link", "b"))
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, filepath.Join(tmp, "a", "b"), identity)
}

func TestResolver_SymlinkLoop(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode on Windows; covered on POSIX")
	}

	resolver, tmp := newOSFSTestResolver(t)
	loopA := filepath.Join(tmp, "loopA")
	loopB := filepath.Join(tmp, "loopB")
	require.NoError(t, os.Symlink(loopB, loopA))
	require.NoError(t, os.Symlink(loopA, loopB))

	_, _, err := resolver.Resolve(loopA)
	assert.ErrorIs(t, err, ErrSymlinkLoop)
}

func TestResolver_SelfReferencingSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires developer mode on Windows; covered on POSIX")
	}

	resolver, tmp := newOSFSTestResolver(t)
	self := filepath.Join(tmp, "self")
	require.NoError(t, os.Symlink(self, self))

	_, _, err := resolver.Resolve(self)
	assert.ErrorIs(t, err, ErrSymlinkLoop)
}

func TestResolver_NonExistentPathUsesLexicalFallback(t *testing.T) {
	t.Parallel()

	resolver, tmp := newOSFSTestResolver(t)
	// The parent exists, only the tail is missing.
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "home"), 0o750))
	missing := filepath.Join(tmp, "home", "missing", "deep", "..", "project")

	identity, exists, err := resolver.Resolve(missing)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, filepath.Join(tmp, "home", "missing", "project"), identity)
}

func TestResolver_EntirelyMissingPath(t *testing.T) {
	t.Parallel()

	resolver, tmp := newOSFSTestResolver(t)
	missing := filepath.Join(tmp, "does", "not", "exist")

	identity, exists, err := resolver.Resolve(missing)
	require.NoError(t, err)
	assert.False(t, exists)
	assert.Equal(t, missing, identity)
}

func TestResolver_HomeExpansion(t *testing.T) {
	t.Parallel()

	home := "/home/tester"
	resolver := NewResolver(afero.NewMemMapFs(), POSIXSemantics{CaseSensitive: true}, home)

	identity, _, err := resolver.Resolve("~")
	require.NoError(t, err)
	assert.Equal(t, home, identity)

	identity, _, err = resolver.Resolve("~/project")
	require.NoError(t, err)
	assert.Equal(t, home+"/project", identity)

	// "~user" is not expanded; it stays as written and only gets cleaned.
	identity, _, err = resolver.Resolve("~other/project")
	require.NoError(t, err)
	assert.Equal(t, "~other/project", identity)
}

func TestResolver_MemMapFsSkipsSymlinkResolution(t *testing.T) {
	t.Parallel()

	// MemMapFs has no ReadlinkIfPossible, so the resolver must degrade to
	// lexical normalization instead of panicking.
	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/home/user/project", 0o755))
	resolver := NewResolver(fs, POSIXSemantics{CaseSensitive: true}, "/home/user")

	identity, exists, err := resolver.Resolve("/home/user/./project/")
	require.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, "/home/user/project", identity)
}

func TestResolver_CaseInsensitiveFolding(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/Users/Test/Project", 0o755))
	resolver := NewResolver(fs, POSIXSemantics{CaseSensitive: false}, "/Users/Test")

	// MemMapFs itself is case sensitive, so existence is looked up with the
	// exact spelling; identity folding is what matters for conflict detection.
	upper, _, err := resolver.Resolve("/USERS/TEST/PROJECT")
	require.NoError(t, err)
	lower, _, err := resolver.Resolve("/users/test/project")
	require.NoError(t, err)
	assert.Equal(t, "/users/test/project", upper)
	assert.Equal(t, lower, upper)

	// Two spellings of an existing directory fold to the same identity.
	first, exists, err := resolver.Resolve("/Users/Test/Project")
	require.NoError(t, err)
	assert.True(t, exists)
	second, exists, err := resolver.Resolve("/users/test/project")
	require.NoError(t, err)
	assert.False(t, exists) // exact spelling absent in the in-memory FS
	assert.Equal(t, first, second)
}
