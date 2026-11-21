// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

// ConfigDirectory is the platform-neutral view of one entry of Finch's
// additional_directories configuration. The platform-specific config package
// converts its own AdditionalDirectory type into this struct so the checker
// stays independent of macOS/Windows types.
type ConfigDirectory struct {
	// Path is the raw host path as written in finch.yaml.
	Path string
	// Writable is the requested permission; nil means the platform default
	// (read-write for historical compatibility).
	Writable *bool
	// MountPoint is the optional guest-side path; empty means "same as host".
	MountPoint string
}

// FromConfigDirectories converts parsed configuration entries into
// Declaration values using their original list positions. The indices are
// deliberately zero-based and contiguous so error messages can cite the exact
// item number (1-based when rendered).
func FromConfigDirectories(dirs []ConfigDirectory) []Declaration {
	declarations := make([]Declaration, 0, len(dirs))
	for i, dir := range dirs {
		mode := ReadWrite
		if dir.Writable != nil && !*dir.Writable {
			mode = ReadOnly
		}
		declarations = append(declarations, Declaration{
			Index:      i,
			HostPath:   dir.Path,
			Mode:       mode,
			MountPoint: dir.MountPoint,
			Source:     SourceNew,
		})
	}
	return declarations
}

// ToSnapshotEntry records a declaration in the form persisted after a
// successful start.
func ToSnapshotEntry(decl Declaration) SnapshotEntry {
	return SnapshotEntry{
		HostPath:   decl.HostPath,
		Writable:   decl.Mode == ReadWrite,
		MountPoint: decl.MountPoint,
	}
}

// ToSnapshotEntries is the slice form of ToSnapshotEntry.
func ToSnapshotEntries(decls []Declaration) []SnapshotEntry {
	entries := make([]SnapshotEntry, 0, len(decls))
	for _, decl := range decls {
		entries = append(entries, ToSnapshotEntry(decl))
	}
	return entries
}
