// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import "strings"

// cleanLexical normalizes an absolute path without touching the file system.
//
// It collapses duplicate separators, resolves "." and ".." components and
// drops trailing separators. On Windows both "/" and "\\" are separators and
// the drive letter is lower-cased. On POSIX only "/" is a separator, so a
// backslash remains part of a file name.
//
// A relative path is returned unchanged: the checker only accepts absolute
// paths and reports relative ones as a separate validation error.
func cleanLexical(input string, sep byte, windows bool) string {
	if input == "" || !isAbsolute(input, windows) {
		return input
	}

	var prefix, rest string
	if windows {
		prefix, rest = splitWindowsPrefix(input)
	} else {
		// Preserve the POSIX root so the cleaned path stays absolute.
		prefix, rest = "/", strings.TrimPrefix(input, "/")
	}

	seps := string(sep)
	if windows {
		seps = "/\\"
	}

	var segments []string
	for _, component := range strings.FieldsFunc(rest, func(r rune) bool {
		return strings.ContainsRune(seps, r)
	}) {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(segments) > 0 {
				segments = segments[:len(segments)-1]
			}
		default:
			segments = append(segments, component)
		}
	}

	cleaned := prefix
	if len(segments) == 0 {
		return cleaned
	}
	if windows {
		return cleaned + strings.Join(segments, `\`)
	}
	return cleaned + strings.Join(segments, "/")
}

func isAbsolute(input string, windows bool) bool {
	if !windows {
		return strings.HasPrefix(input, "/")
	}
	if strings.HasPrefix(input, `\\`) || strings.HasPrefix(input, "//") {
		return true
	}
	return len(input) >= 3 && isDriveLetter(input[0]) && input[1] == ':' &&
		(input[2] == '\\' || input[2] == '/')
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// splitWindowsPrefix separates the rooted part of a Windows path (drive or UNC
// root) from the rest. The returned prefix always ends with a backslash.
func splitWindowsPrefix(input string) (prefix, rest string) {
	switch {
	case strings.HasPrefix(input, `\\`) || strings.HasPrefix(input, "//"):
		// UNC path: \\server\share\... Keep the first two components as root.
		body := strings.TrimLeft(input, `/\`)
		parts := strings.FieldsFunc(body, func(r rune) bool {
			return r == '/' || r == '\\'
		})
		if len(parts) < 2 {
			// A root-only UNC path without a share; keep it verbatim.
			return input, ""
		}
		prefix = `\\` + parts[0] + `\` + parts[1] + `\`
		if len(input) <= len(prefix) {
			return prefix, ""
		}
		rest = input[len(prefix):]
		return prefix, rest
	case len(input) >= 2 && isDriveLetter(input[0]) && input[1] == ':':
		return strings.ToLower(input[:2]) + `\`, input[3:]
	default:
		return `\`, strings.TrimLeft(input, `/\`)
	}
}

// isAncestor implements PathSemantics.IsAncestor for the given separator.
func isAncestor(sep byte, semantics PathSemantics, parent, child string) bool {
	if semantics.Equal(parent, child) {
		return true
	}
	parentSegments := splitSegments(sep, parent)
	childSegments := splitSegments(sep, child)
	if len(parentSegments) >= len(childSegments) {
		return false
	}
	for i, component := range parentSegments {
		if !semantics.Equal(component, childSegments[i]) {
			return false
		}
	}
	return true
}
