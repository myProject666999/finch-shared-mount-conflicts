// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import "strings"

// WindowsSemantics implements PathSemantics for Windows hosts.
//
// Both "/" and "\\" are accepted as separators, drive letters are folded to
// lower case and all comparisons are case insensitive, matching NTFS and the
// Win32 path normalization rules. UNC paths keep their "\\\\server\\share"
// root.
type WindowsSemantics struct {
	// CaseSensitive exists so callers can construct Windows semantics with a
	// case-sensitive volume (for example a mounted ext4 volume). Regular NTFS
	// volumes should leave it false.
	CaseSensitive bool
}

var _ PathSemantics = WindowsSemantics{}

// Separator returns the Windows primary path separator "\\".
func (WindowsSemantics) Separator() byte {
	return '\\'
}

// Clean lexically normalizes a Windows path, accepting both separators and
// lower-casing the drive letter.
func (WindowsSemantics) Clean(path string) string {
	return cleanLexical(path, '\\', true)
}

// Equal compares two cleaned Windows paths, case insensitively by default.
func (s WindowsSemantics) Equal(a, b string) bool {
	if s.CaseSensitive {
		return a == b
	}
	return strings.EqualFold(a, b)
}

// FoldCase lower-cases a cleaned path on a case-insensitive Windows volume.
func (s WindowsSemantics) FoldCase(path string) string {
	if s.CaseSensitive {
		return path
	}
	return strings.ToLower(path)
}

// IsAncestor reports whether parent contains child on a Windows file system.
func (s WindowsSemantics) IsAncestor(parent, child string) bool {
	return isAncestor('\\', s, parent, child)
}
