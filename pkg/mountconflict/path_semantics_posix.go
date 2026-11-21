// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import "strings"

// POSIXSemantics implements PathSemantics for POSIX hosts (Linux and macOS).
//
// A backslash is a valid file-name character on POSIX, never a separator, so
// it is left untouched. CaseSensitive is injected rather than derived from the
// runtime OS: a FAT/exFAT/APFS-case-insensitive volume can be emulated on any
// platform.
type POSIXSemantics struct {
	// CaseSensitive reports whether the host file system distinguishes file
	// names that differ only in case.
	CaseSensitive bool
}

var _ PathSemantics = POSIXSemantics{}

// Separator returns the POSIX path separator "/".
func (POSIXSemantics) Separator() byte {
	return '/'
}

// Clean lexically normalizes a POSIX path. It does not treat backslashes as
// separators, matching kernel behavior.
func (s POSIXSemantics) Clean(path string) string {
	return cleanLexical(path, '/', false)
}

// Equal compares two cleaned POSIX paths, folding case when the file system is
// case insensitive.
func (s POSIXSemantics) Equal(a, b string) bool {
	if s.CaseSensitive {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// FoldCase lower-cases a cleaned path on a case-insensitive file system.
func (s POSIXSemantics) FoldCase(path string) string {
	if s.CaseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// IsAncestor reports whether parent contains child on a POSIX file system.
func (s POSIXSemantics) IsAncestor(parent, child string) bool {
	return isAncestor('/', s, parent, child)
}

// GuestSemantics models the path semantics inside the Linux guest. Guest
// mount points are always POSIX paths and always case sensitive (the guest
// file systems are ext4/virtiofs with case-sensitive lookups).
type GuestSemantics struct{}

var _ PathSemantics = GuestSemantics{}

// Separator returns the guest path separator "/".
func (GuestSemantics) Separator() byte {
	return '/'
}

// Clean lexically normalizes a guest path.
func (GuestSemantics) Clean(path string) string {
	return cleanLexical(path, '/', false)
}

// Equal compares two cleaned guest paths case sensitively.
func (GuestSemantics) Equal(a, b string) bool {
	return a == b
}

// FoldCase is a no-op in the case-sensitive Linux guest.
func (GuestSemantics) FoldCase(path string) string {
	return path
}

// IsAncestor reports whether parent contains child inside the guest.
func (GuestSemantics) IsAncestor(parent, child string) bool {
	return isAncestor('/', GuestSemantics{}, parent, child)
}
