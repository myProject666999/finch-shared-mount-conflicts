// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runfinch/finch/pkg/flog"
	"github.com/runfinch/finch/pkg/mountconflict"
)

// TestSharedMountValidator_RealFilesystem exercises the full pipeline against
// a real directory tree and symlinks, mirroring how vm init/start invoke it.
func TestSharedMountValidator_RealFilesystem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	home := filepath.Join(root, "home")
	project := filepath.Join(home, "project")
	require.NoError(t, os.MkdirAll(project, 0o750))
	alias := filepath.Join(root, "home-alias")
	require.NoError(t, os.Symlink(home, alias))

	validator := NewSharedMountValidator(
		afero.NewOsFs(), flog.NewLogrus(),
		filepath.Join(root, "lima", "_config"), home,
		mountconflict.ExistenceRejectStartup)

	t.Run("read-only home with writable nested project is allowed with a warning", func(t *testing.T) {
		t.Parallel()
		err := validator.Validate([]mountconflict.ConfigDirectory{
			{Path: home, Writable: boolPtr(false)},
			{Path: project, Writable: boolPtr(true)},
		})
		assert.NoError(t, err)
	})

	t.Run("symlink alias and real home with different modes is rejected", func(t *testing.T) {
		t.Parallel()
		err := validator.Validate([]mountconflict.ConfigDirectory{
			{Path: alias, Writable: boolPtr(false)},
			{Path: home, Writable: boolPtr(true)},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "same host directory")
	})

	t.Run("missing directory blocks startup by default", func(t *testing.T) {
		t.Parallel()
		err := validator.Validate([]mountconflict.ConfigDirectory{
			{Path: filepath.Join(root, "unplugged-drive"), Writable: boolPtr(true)},
		})
		require.Error(t, err)
	})
}
