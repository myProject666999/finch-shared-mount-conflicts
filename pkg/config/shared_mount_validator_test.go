// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin || windows

package config

import (
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runfinch/finch/pkg/flog"
	"github.com/runfinch/finch/pkg/mountconflict"
)

func testValidator(fs afero.Fs, policy mountconflict.ExistencePolicy) *SharedMountValidator {
	return NewSharedMountValidator(fs, flog.NewLogrus(), "/lima/_config", "/home/tester", policy)
}

func md(path string, writable bool) mountconflict.ConfigDirectory {
	w := writable
	return mountconflict.ConfigDirectory{Path: path, Writable: &w}
}

func TestSharedMountValidator_AllowCleanConfig(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/home/tester/a", 0o755))
	require.NoError(t, fs.MkdirAll("/home/tester/b", 0o755))

	err := testValidator(fs, mountconflict.ExistenceRejectStartup).Validate(
		[]mountconflict.ConfigDirectory{md("/home/tester/a", true), md("/home/tester/b", false)})
	assert.NoError(t, err)
}

func TestSharedMountValidator_RejectSamePathMixedModes(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/home/tester/data", 0o755))

	err := testValidator(fs, mountconflict.ExistenceRejectStartup).Validate(
		[]mountconflict.ConfigDirectory{md("/home/tester/data", false), md("/home/tester/data", true)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflicts prevent the VM from starting")
	assert.Contains(t, err.Error(), "#1")
	assert.Contains(t, err.Error(), "#2")
}

func TestSharedMountValidator_SnapshotDistinguishesExistingFromNew(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/data", 0o755))
	validator := testValidator(fs, mountconflict.ExistenceRejectStartup)

	// First start: a single read-write entry succeeds and is snapshotted.
	require.NoError(t, validator.Validate([]mountconflict.ConfigDirectory{md("/data", true)}))
	require.NoError(t, validator.SaveSnapshot([]mountconflict.ConfigDirectory{md("/data", true)}))

	// A later config adds the same path read-only; the new entry is rejected
	// while the snapshot entry is reported as already in effect.
	err := validator.Validate([]mountconflict.ConfigDirectory{md("/data", false)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in effect")
}

func TestSharedMountValidator_MissingSnapshotTreatsAllAsNew(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	require.NoError(t, fs.MkdirAll("/data", 0o755))

	err := testValidator(fs, mountconflict.ExistenceRejectStartup).Validate(
		[]mountconflict.ConfigDirectory{md("/data", true)})
	assert.NoError(t, err)
}

func TestSharedMountValidator_RejectMissingDirectory(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	err := testValidator(fs, mountconflict.ExistenceRejectStartup).Validate(
		[]mountconflict.ConfigDirectory{md("/gone", true)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist on the host")
}

func TestSharedMountValidator_SkipWarnPolicy(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	// A missing directory is a warning, not a rejection.
	err := testValidator(fs, mountconflict.ExistenceSkipWarn).Validate(
		[]mountconflict.ConfigDirectory{md("/gone", true)})
	assert.NoError(t, err)
}

func TestSharedMountValidator_SnapshotRoundTrip(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	validator := testValidator(fs, mountconflict.ExistenceRejectStartup)

	dirs := []mountconflict.ConfigDirectory{
		{Path: "/a", Writable: boolPtr(false), MountPoint: "/mnt/a"},
		{Path: "/b", Writable: boolPtr(true)},
	}
	require.NoError(t, validator.SaveSnapshot(dirs))

	store := mountconflict.NewSnapshotStore(fs, "/lima/_config/"+SnapshotFileName)
	snapshot, err := store.Load()
	require.NoError(t, err)
	require.Len(t, snapshot.Entries, 2)
	assert.False(t, snapshot.Entries[0].Writable)
	assert.Equal(t, "/mnt/a", snapshot.Entries[0].MountPoint)
	assert.True(t, snapshot.Entries[1].Writable)
}

func boolPtr(v bool) *bool {
	return &v
}
