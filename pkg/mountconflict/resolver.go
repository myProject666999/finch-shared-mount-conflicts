// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"errors"
	"os"
	"strings"

	"github.com/spf13/afero"
)

// maxSymlinkDepth bounds symlink chain resolution so a link loop cannot run
// forever. Linux's own limit (MAX_NESTED_LINKS) is 40; we use the same value.
const maxSymlinkDepth = 40

// ErrSymlinkLoop is returned when resolving a path visits the same symlink
// more than once, or the chain exceeds maxSymlinkDepth.
var ErrSymlinkLoop = errors.New("symbolic link loop detected while resolving shared directory path")

// linkReader is the optional afero capability needed to read symlinks.
// OsFs implements it; in-memory file systems do not, in which case symlink
// resolution is skipped and lexical normalization is used instead.
type linkReader interface {
	ReadlinkIfPossible(name string) (string, error)
}

type lstatChecker interface {
	LstatIfPossible(name string) (os.FileInfo, bool, error)
}

// Resolver turns a declared host path into a canonical identity that can be
// compared with other declarations.
//
// Resolution order, matching the preference stated in the design:
//  1. expand a leading "~" using the injected home directory;
//  2. lexically normalize with the injected PathSemantics;
//  3. evaluate symbolic links against the real file system;
//  4. fold case on case-insensitive file systems.
//
// A path that does not (yet) exist is not an error: only its existing prefix
// is resolved and the missing tail is kept lexically normalized. Callers that
// need to reject missing directories use the separate existence check so the
// policy stays explicit and testable.
type Resolver struct {
	fs        afero.Fs
	semantics PathSemantics
	homeDir   string
}

// NewResolver builds a Resolver. homeDir is the expanded host home directory
// (already absolute); an empty homeDir disables tilde expansion.
func NewResolver(fs afero.Fs, semantics PathSemantics, homeDir string) *Resolver {
	return &Resolver{fs: fs, semantics: semantics, homeDir: homeDir}
}

// Resolve canonicalizes rawPath. The returned identity is safe to feed into
// PathSemantics.Equal/IsAncestor. A non-existent path is resolved lexically
// and returned with exists == false. A symlink loop returns ErrSymlinkLoop.
func (r *Resolver) Resolve(rawPath string) (identity string, exists bool, err error) {
	expanded := r.expandHome(rawPath)
	lexical := r.semantics.Clean(expanded)

	reader, canReadLinks := r.fs.(linkReader)
	if !canReadLinks {
		return r.semantics.FoldCase(lexical), r.pathExists(lexical), nil
	}

	resolved, err := r.evalSymlinks(lexical, reader)
	if err != nil {
		return "", false, err
	}
	return r.semantics.FoldCase(r.semantics.Clean(resolved)), r.pathExists(lexical), nil
}

// expandHome replaces a leading "~" or "~<sep>" with the host home directory.
// "~user" style expansion is intentionally unsupported (see the design doc's
// platform limitations) and is left untouched.
func (r *Resolver) expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~"+string(r.semantics.Separator())) {
		return path
	}
	if r.homeDir == "" {
		return path
	}
	if path == "~" {
		return r.homeDir
	}
	return r.homeDir + string(r.semantics.Separator()) + path[2:]
}

// evalSymlinks walks the cleaned path component by component. Existing
// components that are symlinks are substituted; the first missing component
// ends resolution and the remaining tail is appended verbatim so non-existent
// paths still get a stable lexical identity.
func (r *Resolver) evalSymlinks(lexical string, reader linkReader) (string, error) {
	sep := string(r.semantics.Separator())

	// resolved holds the already-canonicalized prefix; pending holds the not
	// yet inspected components in their original order.
	resolved := r.rootPrefix(lexical)
	pending := splitSegments(r.semantics.Separator(), lexical)

	visited := make(map[string]struct{})
	depth := 0

	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]
		candidate := r.join(resolved, component, sep)

		target, isLink, exists, err := r.readLink(candidate, reader)
		if err != nil {
			return "", err
		}
		if !isLink {
			if !exists {
				// First missing path element: keep the declared tail verbatim.
				return r.joinAll(candidate, pending, sep), nil
			}
			// Regular existing component: extend the canonical prefix.
			resolved = candidate
			continue
		}

		depth++
		if depth > maxSymlinkDepth {
			return "", ErrSymlinkLoop
		}
		if _, seen := visited[candidate]; seen {
			return "", ErrSymlinkLoop
		}
		visited[candidate] = struct{}{}

		// Substitute the link. An absolute target replaces the whole prefix;
		// a relative target is interpreted from the directory containing the
		// link (resolved, before appending the link name).
		var substituted string
		if r.isAbsoluteTarget(target) {
			substituted = r.semantics.Clean(target)
		} else {
			substituted = r.semantics.Clean(r.join(resolved, target, sep))
		}
		resolved = r.rootPrefix(substituted)
		pending = append(splitSegments(r.semantics.Separator(), substituted), pending...)
	}
	return resolved, nil
}

func (r *Resolver) readLink(path string, reader linkReader) (target string, isLink bool, exists bool, err error) {
	if lstat, ok := r.fs.(lstatChecker); ok {
		info, lstatOK, lstatErr := lstat.LstatIfPossible(path)
		if lstatErr != nil {
			if errors.Is(lstatErr, os.ErrNotExist) {
				return "", false, false, nil
			}
			return "", false, false, lstatErr
		}
		if !lstatOK {
			// File system cannot distinguish symlinks via Lstat; fall through
			// to Readlink and let its result decide.
			exists = true
		} else {
			exists = true
			if info.Mode()&os.ModeSymlink == 0 {
				return "", false, true, nil
			}
		}
	}
	dest, readErr := reader.ReadlinkIfPossible(path)
	if readErr != nil {
		if errors.Is(readErr, os.ErrInvalid) || errors.Is(readErr, os.ErrNotExist) {
			return "", false, exists, nil
		}
		return "", false, exists, readErr
	}
	return dest, true, true, nil
}

func (r *Resolver) pathExists(cleaned string) bool {
	_, err := r.fs.Stat(cleaned)
	return err == nil
}

func (r *Resolver) rootPrefix(cleaned string) string {
	if r.semantics.Separator() == '/' {
		return "/"
	}
	// Windows: drive root "c:\" or UNC root "\\server\share\".
	if strings.HasPrefix(cleaned, `\\`) {
		parts := strings.Split(strings.TrimPrefix(cleaned, `\\`), `\`)
		if len(parts) >= 2 {
			return `\\` + parts[0] + `\` + parts[1] + `\`
		}
	}
	if len(cleaned) >= 3 && isDriveLetter(cleaned[0]) && cleaned[1] == ':' {
		return cleaned[:3]
	}
	return `\`
}

func (r *Resolver) isAbsoluteTarget(target string) bool {
	return isAbsolute(target, r.semantics.Separator() == '\\')
}

func (r *Resolver) join(base, tail, sep string) string {
	if strings.HasSuffix(base, sep) {
		return base + tail
	}
	return base + sep + tail
}

func (r *Resolver) joinAll(base string, components []string, sep string) string {
	if len(components) == 0 {
		return base
	}
	return base + sep + strings.Join(components, sep)
}
