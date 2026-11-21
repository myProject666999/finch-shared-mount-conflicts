// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import "fmt"

// Mode is the requested write permission of a shared directory.
type Mode int

const (
	// ReadOnly mounts the directory so the guest cannot modify its contents.
	ReadOnly Mode = iota
	// ReadWrite mounts the directory with guest write access.
	ReadWrite
)

// String renders the mode as it appears in error messages and config files.
func (m Mode) String() string {
	switch m {
	case ReadOnly:
		return "read-only"
	case ReadWrite:
		return "read-write"
	default:
		return fmt.Sprintf("unknown(%d)", int(m))
	}
}

// Relation classifies how two resolved host paths, or two guest mount points,
// relate to each other.
type Relation int

const (
	// Unrelated paths share no prefix.
	Unrelated Relation = iota
	// SameEntity paths resolve to the exact same file system location.
	SameEntity
	// AncestorDescendant means one path is strictly above the other.
	AncestorDescendant
)

func (r Relation) String() string {
	switch r {
	case SameEntity:
		return "same directory"
	case AncestorDescendant:
		return "nested directories (one contains the other)"
	case Unrelated:
		return "unrelated directories"
	default:
		return fmt.Sprintf("unknown(%d)", int(r))
	}
}

// Verdict is the decision the checker returns for a pair of declarations.
type Verdict int

const (
	// VerdictAllow means no conflict; the pair can be applied as written.
	VerdictAllow Verdict = iota
	// VerdictDedup means the two declarations are redundant; the later one is
	// dropped while keeping the first declaration's position.
	VerdictDedup
	// VerdictWarn means permitted but potentially surprising; startup
	// continues and the message explains the effective behavior.
	VerdictWarn
	// VerdictReject means ambiguous or unsafe; startup must be refused until
	// the configuration is fixed.
	VerdictReject
)

// String renders the verdict for reports.
func (v Verdict) String() string {
	switch v {
	case VerdictAllow:
		return "allow"
	case VerdictDedup:
		return "deduplicate"
	case VerdictWarn:
		return "warn"
	case VerdictReject:
		return "reject"
	default:
		return fmt.Sprintf("unknown(%d)", int(v))
	}
}

// Source distinguishes declarations that were already in effect from newly
// added ones.
type Source int

const (
	// SourceExisting comes from the last successful-start snapshot.
	SourceExisting Source = iota
	// SourceNew is part of the configuration currently being validated.
	SourceNew
	// SourceImplicit is a mount Finch/Lima adds without a user declaration
	// (for example the default read-only home directory).
	SourceImplicit
)

func (s Source) String() string {
	switch s {
	case SourceExisting:
		return "already in effect"
	case SourceNew:
		return "newly added"
	case SourceImplicit:
		return "implicit default mount"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Declaration is one shared-directory entry as written by the user.
//
// Index is the zero-based position in the original configuration list and is
// preserved through every report so messages name the exact entry the user
// has to edit.
type Declaration struct {
	// Index is the position in the original additional_directories list.
	Index int
	// HostPath is the raw, unexpanded host-side path from the config.
	HostPath string
	// Mode is the requested read-only/read-write permission.
	Mode Mode
	// MountPoint is the guest-side path. Empty means "same as HostPath",
	// which the checker expands before comparison.
	MountPoint string
	// Source marks implicit/snapshot entries. User declarations are SourceNew.
	Source Source
}

// Finding describes the outcome of comparing two declarations.
type Finding struct {
	// Kind identifies which rule of the decision table fired.
	Kind RelationKind
	// Verdict is allow/dedup/warn/reject.
	Verdict Verdict
	// First and Second are the conflicting pair, each carrying its original
	// config index. First is always the declaration with the smaller index.
	First, Second Declaration
	// Message is a human-readable explanation and remediation hint.
	Message string
	// Remediation tells the user concretely how to resolve the finding.
	Remediation string
}

// RelationKind disambiguates whether a finding concerns host paths or guest
// mount points, since the same pair can conflict on both sides.
type RelationKind int

const (
	// KindHostPath means the two host locations are the same entity or nested.
	KindHostPath RelationKind = iota
	// KindMountPoint means two different host locations target one guest path.
	KindMountPoint
)

func (k RelationKind) String() string {
	switch k {
	case KindHostPath:
		return "host path"
	case KindMountPoint:
		return "guest mount point"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// Report is the full result of validating a set of declarations.
type Report struct {
	Findings []Finding
}

// Rejects reports whether at least one finding blocks startup.
func (r *Report) Rejects() bool {
	for _, f := range r.Findings {
		if f.Verdict == VerdictReject {
			return true
		}
	}
	return false
}

// BlockingFindings returns only the findings that must stop startup.
func (r *Report) BlockingFindings() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Verdict == VerdictReject {
			out = append(out, f)
		}
	}
	return out
}

// Warnings returns non-blocking findings (warn or dedup) for logging.
func (r *Report) Warnings() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Verdict == VerdictWarn || f.Verdict == VerdictDedup {
			out = append(out, f)
		}
	}
	return out
}
