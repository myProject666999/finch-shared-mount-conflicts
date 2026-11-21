// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package mountconflict

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPOSIXSemantics_Clean(t *testing.T) {
	t.Parallel()

	sem := POSIXSemantics{CaseSensitive: true}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain absolute path", in: "/home/user/project", want: "/home/user/project"},
		{name: "trailing slash is removed", in: "/home/user/project/", want: "/home/user/project"},
		{name: "multiple trailing slashes", in: "/home/user/project///", want: "/home/user/project"},
		{name: "duplicate separators collapse", in: "/home//user///project", want: "/home/user/project"},
		{name: "single dot segments removed", in: "/home/./user/./project", want: "/home/user/project"},
		{name: "double dot segments resolved", in: "/home/user/../user/project", want: "/home/user/project"},
		{name: "double dot at root stays at root", in: "/../home", want: "/home"},
		{name: "root stays root", in: "/", want: "/"},
		{name: "backslash is a file name character, not a separator", in: "/home/user/my\\dir", want: "/home/user/my\\dir"},
		{name: "relative path is returned untouched", in: "relative/path", want: "relative/path"},
		{name: "empty string", in: "", want: ""},
		{name: "dot only", in: "/.", want: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sem.Clean(tt.in))
		})
	}
}

func TestPOSIXSemantics_EqualAndAncestor(t *testing.T) {
	t.Parallel()

	caseSensitive := POSIXSemantics{CaseSensitive: true}
	caseInsensitive := POSIXSemantics{CaseSensitive: false}

	assert.True(t, caseSensitive.Equal("/Home/User", "/Home/User"))
	assert.False(t, caseSensitive.Equal("/Home/User", "/home/user"))
	assert.True(t, caseInsensitive.Equal("/Home/User", "/home/user"))

	assert.True(t, caseSensitive.IsAncestor("/home", "/home/user/project"))
	assert.True(t, caseSensitive.IsAncestor("/home", "/home"))
	assert.False(t, caseSensitive.IsAncestor("/home/user", "/home"))
	assert.False(t, caseSensitive.IsAncestor("/home/user", "/home/user2"))
	assert.True(t, caseInsensitive.IsAncestor("/HOME", "/home/user"))
}

func TestWindowsSemantics_Clean(t *testing.T) {
	t.Parallel()

	sem := WindowsSemantics{}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "drive path with backslashes", in: `C:\Users\user`, want: `c:\Users\user`},
		{name: "drive letter is lower-cased", in: `D:\Data`, want: `d:\Data`},
		{name: "forward slashes converted", in: `C:/Users/user/`, want: `c:\Users\user`},
		{name: "dots and double dots resolved", in: `C:\Users\user\..\admin\project`, want: `c:\Users\admin\project`},
		{name: "duplicate separators collapse", in: `C:\\\\Users\\user`, want: `c:\Users\user`},
		{name: "trailing slash removed", in: `C:\Users\user\`, want: `c:\Users\user`},
		{name: "unc root preserved", in: `\\server\share\dir`, want: `\\server\share\dir`},
		{name: "unc with forward slashes", in: `//server/share/dir/`, want: `\\server\share\dir`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sem.Clean(tt.in))
		})
	}
}

func TestWindowsSemantics_EqualAndAncestor(t *testing.T) {
	t.Parallel()

	sem := WindowsSemantics{}

	// Only equivalent spellings on Windows compare equal. Inputs to Equal are
	// always run through Clean first, matching how the checker uses them.
	assert.True(t, sem.Equal(sem.Clean(`c:\Users\user`), sem.Clean(`C:\USERS\USER`)))
	assert.True(t, sem.Equal(sem.Clean(`c:\Users\user`), sem.Clean(`C:/Users/user`)))
	assert.True(t, sem.IsAncestor(sem.Clean(`c:\Users`), sem.Clean(`c:\Users\user\project`)))
	assert.False(t, sem.IsAncestor(sem.Clean(`c:\Users2`), sem.Clean(`c:\Users\user`)))
	assert.True(t, sem.IsAncestor(sem.Clean(`\\server\share`), sem.Clean(`\\server\share\sub`)))
}

func TestGuestSemantics(t *testing.T) {
	t.Parallel()

	sem := GuestSemantics{}
	assert.Equal(t, "/mnt/data/project", sem.Clean("/mnt/data/./project/"))
	assert.True(t, sem.Equal("/mnt/data", "/mnt/data"))
	assert.False(t, sem.Equal("/mnt/Data", "/mnt/data"))
	assert.True(t, sem.IsAncestor("/mnt/data", "/mnt/data/sub"))
	assert.False(t, sem.IsAncestor("/mnt/data2", "/mnt/data"))
}

func TestPlatformSpecificEquivalenceIsNotCrossApplied(t *testing.T) {
	t.Parallel()

	// A backslash inside a POSIX file name must never be treated as a path
	// separator, so two different file names must not compare as nested.
	posix := POSIXSemantics{CaseSensitive: true}
	a := posix.Clean("/home/user/my\\dir")
	b := posix.Clean("/home/user/my")
	assert.False(t, posix.IsAncestor(b, a))
	assert.False(t, posix.Equal(a, b))

	// The same strings on Windows semantics do use the backslash as separator.
	win := WindowsSemantics{}
	wa := win.Clean(`C:\Users\user\my\dir`)
	wb := win.Clean(`C:\Users\user\my`)
	assert.True(t, win.IsAncestor(wb, wa))
}
