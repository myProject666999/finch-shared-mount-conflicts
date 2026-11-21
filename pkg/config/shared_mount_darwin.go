// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package config

import (
	"path/filepath"

	"github.com/runfinch/finch/pkg/flog"
	"github.com/runfinch/finch/pkg/mountconflict"
)

// platformMountOptions holds the shared-mount settings that only exist on
// macOS, where user-defined additional_directories are applied to Lima mounts.
type platformMountOptions struct {
	homeDir         string
	existencePolicy mountconflict.ExistencePolicy
}

// applyPlatformMountOptions attaches macOS-only mount settings. Embedding a
// named struct keeps these fields out of the Windows build while sharing the
// limaConfigApplier type across platforms.
func applyPlatformMountOptions(applier *limaConfigApplier, homeDir string) {
	applier.platformMountOptions = platformMountOptions{
		homeDir: homeDir,
		// Refuse to start when a declared directory is missing. The trade-off
		// (an unplugged external disk also blocks startup) is documented in
		// docs/design/shared_directory_conflict_checks.md.
		existencePolicy: mountconflict.ExistenceRejectStartup,
	}
}

// sharedMountValidator builds the validator from the applier's dependencies.
func (lca *limaConfigApplier) sharedMountValidator(logger flog.Logger) *SharedMountValidator {
	return NewSharedMountValidator(
		lca.fs,
		logger,
		filepath.Dir(lca.limaOverrideConfigPath),
		lca.homeDir,
		lca.existencePolicy,
	)
}

// ToConfigDirectories converts the darwin additional_directories entries into
// the platform-neutral representation consumed by the conflict checker.
func ToConfigDirectories(dirs []AdditionalDirectory) []mountconflict.ConfigDirectory {
	out := make([]mountconflict.ConfigDirectory, 0, len(dirs))
	for _, dir := range dirs {
		entry := mountconflict.ConfigDirectory{}
		if dir.Path != nil {
			entry.Path = *dir.Path
		}
		entry.Writable = dir.Writable
		if dir.MountPoint != nil {
			entry.MountPoint = *dir.MountPoint
		}
		out = append(out, entry)
	}
	return out
}

// ValidateSharedMounts runs the conflict decision table before Lima config is
// regenerated, so ambiguous read-only/read-write combinations block startup
// instead of producing a VM whose effective permissions depend on ordering.
func (lca *limaConfigApplier) ValidateSharedMounts(logger flog.Logger) error {
	validator := lca.sharedMountValidator(logger)
	return validator.Validate(ToConfigDirectories(lca.cfg.AdditionalDirectories))
}

// PersistSharedMountSnapshot records the active declarations after a
// successful start.
func (lca *limaConfigApplier) PersistSharedMountSnapshot() error {
	validator := lca.sharedMountValidator(flog.NewLogrus())
	return validator.SaveSnapshot(ToConfigDirectories(lca.cfg.AdditionalDirectories))
}
