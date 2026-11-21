// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

// Package mountconflict validates shared-directory declarations before a VM is
// started.
//
// All checks in this package are platform independent: host path semantics are
// injected through PathSemantics so the same logic can be tested on any
// operating system, including emulating case-insensitive file systems and
// Windows drive letters.
package mountconflict

import "strings"

// PathSemantics captures the lexical and case-folding rules of a host file
// system. Implementations must not touch the disk; comparing two paths is a
// pure function of the strings and the CaseSensitive flag.
type PathSemantics interface {
	// Separator returns the primary path separator of the platform, e.g. "/"
	// on POSIX and "\\" on Windows.
	Separator() byte
	// Clean lexically normalizes an absolute path. It resolves ".", "..",
	// duplicate separators and trailing separators without touching the disk.
	Clean(path string) string
	// Equal reports whether two already-cleaned paths name the same location
	// on a file system with these semantics. On a case-insensitive file
	// system the comparison folds case, including Windows drive letters.
	Equal(a, b string) bool
	// FoldCase returns the canonical spelling for case-insensitive
	// comparison. On a case-sensitive file system the input is unchanged.
	FoldCase(path string) string
	// IsAncestor reports whether cleaned path parent is equal to, or an
	// ancestor directory of, cleaned path child.
	IsAncestor(parent, child string) bool
}

// splitSegments splits a cleaned absolute path into its non-empty
// components. The root itself has no components.
func splitSegments(sep byte, cleaned string) []string {
	trimmed := strings.Trim(cleaned, string(sep))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(sep))
}
