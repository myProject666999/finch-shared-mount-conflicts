// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"fmt"

	"github.com/spf13/afero"
)

// ExistencePolicy decides what to do with a declared directory that is absent
// on the host.
type ExistencePolicy int

const (
	// ExistenceRejectStartup refuses to start. This is the default: silently
	// skipping a missing share can make guest programs write to the wrong
	// place when an external disk is unplugged.
	ExistenceRejectStartup ExistencePolicy = iota
	// ExistenceSkipWarn drops the declaration and logs a warning so the VM
	// still boots (for example an external drive that is occasionally
	// disconnected).
	ExistenceSkipWarn
	// ExistenceAutoCreate creates the directory before starting.
	ExistenceAutoCreate
)

// Checker validates shared-directory declarations against the decision table.
// It is safe for concurrent use as long as the underlying afero.Fs is.
type Checker struct {
	resolver       *Resolver
	hostSemantics  PathSemantics
	guestSemantics PathSemantics
	existence      ExistencePolicy
	fs             afero.Fs
}

// NewChecker builds a Checker. guestSemantics is normally GuestSemantics{}
// because the guest is Linux; it stays injectable for tests.
func NewChecker(
	fs afero.Fs,
	hostSemantics PathSemantics,
	guestSemantics PathSemantics,
	resolver *Resolver,
	existence ExistencePolicy,
) *Checker {
	return &Checker{
		resolver:       resolver,
		hostSemantics:  hostSemantics,
		guestSemantics: guestSemantics,
		existence:      existence,
		fs:             fs,
	}
}

// resolvedEntry pairs a declaration with its canonical identities and its
// stable position in the combined input. Order is always carried by slice
// position, never by Declaration.Index: when a snapshot is combined with new
// declarations, each side keeps its own original index, so the rendered item
// number still matches the user's configuration file.
type resolvedEntry struct {
	decl          Declaration
	order         int
	hostIdentity  string
	mountIdentity string
	exists        bool
}

// Check runs every rule of the decision table. All violations are collected in
// one pass; nothing short-circuits. Findings are emitted in the deterministic
// order of the combined declaration slice (snapshot entries first, then new
// entries in their original order), never in a path-sorted order, so the
// reported item numbers match what the user wrote.
func (c *Checker) Check(declarations []Declaration) (*Report, error) {
	entries, hardErr := c.resolveAll(declarations)
	if hardErr != nil {
		return nil, hardErr
	}

	report := &Report{}
	report.Findings = append(report.Findings, c.checkExistence(entries)...)

	// Pairwise comparison in combined-slice order: existence findings and
	// pair findings therefore share one stable, reproducible ordering.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			report.Findings = append(report.Findings, c.evaluatePair(entries[i], entries[j])...)
		}
	}
	return report, nil
}

func (c *Checker) resolveAll(declarations []Declaration) ([]resolvedEntry, error) {
	entries := make([]resolvedEntry, 0, len(declarations))
	for position, decl := range declarations {
		hostIdentity, exists, err := c.resolver.Resolve(decl.HostPath)
		if err != nil {
			return nil, fmt.Errorf(
				"shared directory entry #%d (%q) cannot be resolved: %w; "+
					"note that check results only describe the file system at check time",
				decl.Index+1, decl.HostPath, err)
		}
		mountPoint := decl.MountPoint
		if mountPoint == "" {
			mountPoint = hostIdentity
		}
		mountIdentity := c.guestSemantics.Clean(mountPoint)
		entries = append(entries, resolvedEntry{
			decl:          decl,
			order:         position,
			hostIdentity:  hostIdentity,
			mountIdentity: mountIdentity,
			exists:        exists,
		})
	}
	return entries, nil
}

func (c *Checker) checkExistence(entries []resolvedEntry) []Finding {
	var findings []Finding
	for _, entry := range entries {
		if entry.exists {
			continue
		}
		switch c.existence {
		case ExistenceRejectStartup:
			findings = append(findings, Finding{
				Kind:    KindHostPath,
				Verdict: VerdictReject,
				First:   entry.decl,
				Second:  Declaration{},
				Message: fmt.Sprintf(
					"shared directory entry #%d (%q) does not exist on the host",
					entry.decl.Index+1, entry.decl.HostPath),
				Remediation: "create the directory, plug in the disk that contains it, " +
					"or remove/comment out the entry before starting the VM",
			})
		case ExistenceSkipWarn:
			findings = append(findings, Finding{
				Kind:    KindHostPath,
				Verdict: VerdictWarn,
				First:   entry.decl,
				Message: fmt.Sprintf(
					"shared directory entry #%d (%q) does not exist on the host; it will be skipped",
					entry.decl.Index+1, entry.decl.HostPath),
				Remediation: "create the directory or remove the entry to silence this warning",
			})
		case ExistenceAutoCreate:
			if err := c.fs.MkdirAll(entry.hostIdentity, 0o755); err != nil {
				findings = append(findings, Finding{
					Kind:    KindHostPath,
					Verdict: VerdictReject,
					First:   entry.decl,
					Message: fmt.Sprintf(
						"shared directory entry #%d (%q) does not exist and could not be created: %v",
						entry.decl.Index+1, entry.decl.HostPath, err),
					Remediation: "check permissions on the parent directory, or create the directory manually",
				})
			}
		}
	}
	return findings
}

// evaluatePair applies the decision table to one ordered pair.
func (c *Checker) evaluatePair(a, b resolvedEntry) []Finding {
	var findings []Finding

	hostRelation := c.relate(c.hostSemantics, a.hostIdentity, b.hostIdentity)
	if hostRelation != Unrelated {
		if f, ok := c.hostPathFinding(a, b, hostRelation); ok {
			findings = append(findings, f)
		}
	}

	mountRelation := c.relate(c.guestSemantics, a.mountIdentity, b.mountIdentity)
	if mountRelation != Unrelated {
		if f, ok := c.mountPointFinding(a, b, hostRelation, mountRelation); ok {
			findings = append(findings, f)
		}
	}

	return findings
}

func (c *Checker) relate(semantics PathSemantics, x, y string) Relation {
	switch {
	case semantics.Equal(x, y):
		return SameEntity
	case semantics.IsAncestor(x, y) || semantics.IsAncestor(y, x):
		return AncestorDescendant
	default:
		return Unrelated
	}
}

// hostPathFinding implements the host-side half of the decision table.
func (c *Checker) hostPathFinding(a, b resolvedEntry, relation Relation) (Finding, bool) {
	switch relation {
	case SameEntity:
		return c.sameEntityHostFinding(a, b)
	case AncestorDescendant:
		return c.nestedHostFinding(a, b)
	default:
		return Finding{}, false
	}
}

func (c *Checker) sameEntityHostFinding(a, b resolvedEntry) (Finding, bool) {
	if a.decl.Mode == b.decl.Mode {
		return Finding{
			Kind:        KindHostPath,
			Verdict:     VerdictDedup,
			First:       a.decl,
			Second:      b.decl,
			Message:     c.dupMessage(a, b, "resolve to the same host directory with the same permission"),
			Remediation: fmt.Sprintf("remove duplicate entry #%d", b.decl.Index+1),
		}, true
	}
	return Finding{
		Kind:    KindHostPath,
		Verdict: VerdictReject,
		First:   a.decl,
		Second:  b.decl,
		Message: c.conflictMessage(a, b,
			"resolve to the same host directory but request different permissions",
			"the directory cannot be read-only and read-write at the same time"),
		Remediation: fmt.Sprintf(
			"make entries #%d and #%d use the same permission, or remove one of them",
			a.decl.Index+1, b.decl.Index+1),
	}, true
}

func (c *Checker) nestedHostFinding(a, b resolvedEntry) (Finding, bool) {
	ancestor, descendant := orderAncestorDescendant(a, b, c.hostSemantics)

	switch {
	case ancestor.decl.Mode == ReadOnly && descendant.decl.Mode == ReadWrite:
		return Finding{
			Kind:    KindHostPath,
			Verdict: VerdictWarn,
			First:   a.decl,
			Second:  b.decl,
			Message: c.conflictMessage(a, b,
				"are nested and ask for a read-write directory inside a read-only directory",
				"the nested mount is applied after its parent, so the descendant stays writable; "+
					"see the design doc for backend-specific caveats (virtiofs/reverse-sshfs)"),
			Remediation: fmt.Sprintf(
				"if the whole tree should be read-only, change entry #%d to read-only; "+
					"if the nested writable directory is intentional, no action is needed",
				descendant.decl.Index+1),
		}, true
	case ancestor.decl.Mode == ReadWrite && descendant.decl.Mode == ReadOnly:
		return Finding{
			Kind:    KindHostPath,
			Verdict: VerdictWarn,
			First:   a.decl,
			Second:  b.decl,
			Message: c.conflictMessage(a, b,
				"are nested and ask for a read-only directory inside a read-write directory",
				"the nested read-only mount covers the writable parent for that subtree, "+
					"which is the supported way to restrict part of a writable share"),
			Remediation: "no action is needed; the nested entry deliberately tightens permissions",
		}, true
	default:
		// RO/RO and RW/RW nesting: redundant overlap, no ambiguity.
		return Finding{
			Kind:    KindHostPath,
			Verdict: VerdictWarn,
			First:   a.decl,
			Second:  b.decl,
			Message: c.dupMessage(a, b, "are nested directories with the same permission"),
			Remediation: fmt.Sprintf("entry #%d is already covered by #%d; consider removing the narrower entry",
				descendant.decl.Index+1, ancestor.decl.Index+1),
		}, true
	}
}

// mountPointFinding implements the guest-side half of the decision table.
func (c *Checker) mountPointFinding(a, b resolvedEntry, hostRelation, mountRelation Relation) (Finding, bool) {
	// Same host entity already produced a host-side finding; the mount point
	// adds no new information there.
	if hostRelation == SameEntity {
		return Finding{}, false
	}

	if mountRelation == SameEntity {
		// Two distinct host locations mapped onto one guest path. The later
		// mount hides whatever the earlier one put there.
		return Finding{
			Kind:    KindMountPoint,
			Verdict: VerdictReject,
			First:   a.decl,
			Second:  b.decl,
			Message: c.mountConflictMessage(a, b,
				"two different host directories are mounted at the same guest path",
				"one mount would hide the other and the visible content depends on ordering"),
			Remediation: fmt.Sprintf(
				"give entry #%d a different mount point, or remove one of the entries",
				b.decl.Index+1),
		}, true
	}

	// Nested mount points backed by different host directories: normal Linux
	// mount stacking, always allowed.
	return Finding{}, false
}

func orderAncestorDescendant(a, b resolvedEntry, semantics PathSemantics) (ancestor, descendant resolvedEntry) {
	if semantics.IsAncestor(a.hostIdentity, b.hostIdentity) {
		return a, b
	}
	return b, a
}

func (c *Checker) dupMessage(a, b resolvedEntry, detail string) string {
	return fmt.Sprintf("shared directory entries #%d (%q) and #%d (%q) %s",
		a.decl.Index+1, a.decl.HostPath, b.decl.Index+1, b.decl.HostPath, detail)
}

func (c *Checker) conflictMessage(a, b resolvedEntry, detail, effect string) string {
	return fmt.Sprintf(
		"shared directory entries #%d (%q, %s, %s) and #%d (%q, %s, %s) %s: %s",
		a.decl.Index+1, a.decl.HostPath, a.decl.Mode, a.decl.Source,
		b.decl.Index+1, b.decl.HostPath, b.decl.Mode, b.decl.Source,
		detail, effect)
}

func (c *Checker) mountConflictMessage(a, b resolvedEntry, detail, effect string) string {
	return fmt.Sprintf(
		"shared directory entries #%d (host %q -> guest %q) and #%d (host %q -> guest %q) %s: %s",
		a.decl.Index+1, a.decl.HostPath, a.mountIdentity,
		b.decl.Index+1, b.decl.HostPath, b.mountIdentity, detail, effect)
}
