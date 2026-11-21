// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin || windows

package config

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/spf13/afero"

	"github.com/runfinch/finch/pkg/flog"
	"github.com/runfinch/finch/pkg/mountconflict"
)

// SnapshotFileName is the file, next to the Lima override config, that records
// the shared directories in effect after the last successful VM start.
const SnapshotFileName = "shared-directory-snapshot.yaml"

// SharedMountValidator runs the conflict decision table before a VM starts.
type SharedMountValidator struct {
	fs        afero.Fs
	logger    flog.Logger
	semantics mountconflict.PathSemantics
	homeDir   string
	snapPath  string
	policy    mountconflict.ExistencePolicy
}

// NewSharedMountValidator wires the platform path semantics and snapshot
// location. homeDir is the expanded host home directory (for "~" expansion).
func NewSharedMountValidator(
	fs afero.Fs,
	logger flog.Logger,
	limaConfigDir, homeDir string,
	policy mountconflict.ExistencePolicy,
) *SharedMountValidator {
	semantics := hostSemantics()
	snapshotPath := filepath.Join(limaConfigDir, SnapshotFileName)
	return &SharedMountValidator{
		fs:        fs,
		logger:    logger,
		semantics: semantics,
		homeDir:   homeDir,
		snapPath:  snapshotPath,
		policy:    policy,
	}
}

func hostSemantics() mountconflict.PathSemantics {
	if runtime.GOOS == "windows" {
		// NTFS and the Win32 namespace are case insensitive. Volumes mounted
		// from a case-sensitive file system are a documented limitation.
		return mountconflict.WindowsSemantics{CaseSensitive: false}
	}
	// macOS HFS+/APFS are case insensitive by default but case-preserving;
	// APFS can be formatted case sensitive, which is a documented limitation
	// handled by injecting CaseSensitive explicitly in tests.
	return mountconflict.POSIXSemantics{CaseSensitive: false}
}

func (v *SharedMountValidator) buildChecker() *mountconflict.Checker {
	resolver := mountconflict.NewResolver(v.fs, v.semantics, v.homeDir)
	return mountconflict.NewChecker(v.fs, v.semantics, mountconflict.GuestSemantics{}, resolver, v.policy)
}

// Validate runs the full decision table against the current declarations and
// the last successful-start snapshot. It returns an error when at least one
// finding blocks startup; warnings and notices are logged.
func (v *SharedMountValidator) Validate(dirs []mountconflict.ConfigDirectory) error {
	checker := v.buildChecker()
	newDecls := mountconflict.FromConfigDirectories(dirs)

	store := mountconflict.NewSnapshotStore(v.fs, v.snapPath)
	snapshot, loadErr := store.Load()
	if loadErr != nil {
		// A missing snapshot is an expected first-run condition, not a failure.
		snapshot = nil
	}

	report, notice := checker.CheckAgainstSnapshot(newDecls, snapshot)
	if notice != "" {
		v.logger.Warnln(notice)
	}
	for _, warning := range report.Warnings() {
		v.logger.Warnln(warning.Message)
		if warning.Remediation != "" {
			v.logger.Warnf("suggested action: %s", warning.Remediation)
		}
	}

	if report.Rejects() {
		return v.formatRejection(report.BlockingFindings())
	}
	return nil
}

// SaveSnapshot persists the declarations after a successful start so the next
// check can tell existing entries from newly added ones.
func (v *SharedMountValidator) SaveSnapshot(dirs []mountconflict.ConfigDirectory) error {
	store := mountconflict.NewSnapshotStore(v.fs, v.snapPath)
	return store.Save(mountconflict.ToSnapshotEntries(mountconflict.FromConfigDirectories(dirs)))
}

func (v *SharedMountValidator) formatRejection(findings []mountconflict.Finding) error {
	message := "shared directory configuration conflicts prevent the VM from starting:\n"
	for _, f := range findings {
		message += fmt.Sprintf("- %s\n  how to fix: %s\n", f.Message, f.Remediation)
	}
	return fmt.Errorf("%s", message)
}
