// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package config

import (
	"github.com/runfinch/finch/pkg/flog"
)

// platformMountOptions is empty on Windows because user-defined
// additional_directories are not yet wired into the WSL mount model. Its
// methods still provide the shared validation entry points so the
// limaConfigApplier type and its callers stay identical across platforms.
type platformMountOptions struct{}

// applyPlatformMountOptions is a no-op counterpart to the macOS implementation.
func applyPlatformMountOptions(_ *limaConfigApplier, _ string) {}

// validate is the Windows no-op shared-mount check.
func (platformMountOptions) validate(_ flog.Logger) error {
	return nil
}

// persist is the Windows no-op snapshot writer.
func (platformMountOptions) persist() error {
	return nil
}

// ValidateSharedMounts is a no-op on Windows until user-defined WSL shares are
// introduced; see docs/design/shared_directory_conflict_checks.md for the
// known limitation.
func (lca *limaConfigApplier) ValidateSharedMounts(logger flog.Logger) error {
	return lca.validate(logger)
}

// PersistSharedMountSnapshot is a no-op counterpart to ValidateSharedMounts.
func (lca *limaConfigApplier) PersistSharedMountSnapshot() error {
	return lca.persist()
}
