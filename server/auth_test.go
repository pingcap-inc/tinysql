// Copyright 2026 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/util"
)

type testAuthSuite struct{}

var _ = Suite(&testAuthSuite{})

// scramble computes a client's mysql_native_password response.
func scramble(salt []byte, password string) []byte {
	stage1 := sha1.Sum([]byte(password))
	stage2 := sha1.Sum(stage1[:])
	h := sha1.New()
	h.Write(salt)
	h.Write(stage2[:])
	out := h.Sum(nil)
	for i := range out {
		out[i] ^= stage1[i]
	}
	return out
}

func (s *testAuthSuite) TestCheckNativePassword(c *C) {
	salt := util.RandomBuf(20)
	stored := NativePasswordHash("secret")
	c.Assert(checkNativePassword(salt, scramble(salt, "secret"), stored), IsTrue)
	c.Assert(checkNativePassword(salt, scramble(salt, "Secret"), stored), IsFalse)
	c.Assert(checkNativePassword(util.RandomBuf(20), scramble(salt, "secret"), stored), IsFalse)
	c.Assert(checkNativePassword(salt, nil, stored), IsFalse)
	c.Assert(checkNativePassword(salt, scramble(salt, "secret")[:19], stored), IsFalse)
	c.Assert(checkNativePassword(salt, scramble(salt, "secret"), nil), IsFalse)
}

func (s *testAuthSuite) TestRandomBufIsSaltSafe(c *C) {
	buf := util.RandomBuf(1000)
	for _, b := range buf {
		c.Assert(b != 0 && b != '$' && b < 0x80, IsTrue)
	}
}

func (s *testAuthSuite) TestLoadOrCreateRootPassword(c *C) {
	dir := filepath.Join(c.MkDir(), "data")
	pw, file, err := LoadOrCreateRootPassword(dir)
	c.Assert(err, IsNil)
	c.Assert(len(pw), Equals, generatedPasswordLen)
	c.Assert(file, Equals, filepath.Join(dir, RootPasswordFileName))

	// The same password is reused on the next start.
	pw2, _, err := LoadOrCreateRootPassword(dir)
	c.Assert(err, IsNil)
	c.Assert(pw2, Equals, pw)

	if runtime.GOOS == "windows" {
		return
	}
	fi, err := os.Stat(file)
	c.Assert(err, IsNil)
	c.Assert(fi.Mode().Perm(), Equals, os.FileMode(0600))
	fi, err = os.Stat(dir)
	c.Assert(err, IsNil)
	c.Assert(fi.Mode().Perm(), Equals, os.FileMode(0700))

	// A file readable by others is refused.
	c.Assert(os.Chmod(file, 0644), IsNil)
	_, _, err = LoadOrCreateRootPassword(dir)
	c.Assert(err, NotNil)

	// A symlink in place of the file is refused.
	c.Assert(os.Remove(file), IsNil)
	target := filepath.Join(c.MkDir(), "elsewhere")
	c.Assert(os.WriteFile(target, []byte("planted\n"), 0600), IsNil)
	c.Assert(os.Symlink(target, file), IsNil)
	_, _, err = LoadOrCreateRootPassword(dir)
	c.Assert(err, NotNil)

	// A group-writable data directory is refused.
	c.Assert(os.Remove(file), IsNil)
	c.Assert(os.Chmod(dir, 0770), IsNil)
	_, _, err = LoadOrCreateRootPassword(dir)
	c.Assert(err, NotNil)
}
