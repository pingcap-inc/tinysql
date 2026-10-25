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

package kv

import (
	. "github.com/pingcap/check"
)

// Deletes are buffered entries too and count against the size limits.
func (s *testKVSuite) TestDeleteRespectsSizeLimits(c *C) {
	buf := NewMemDbBuffer(DefaultTxnMembufCap).(*memDbBuffer)
	buf.entrySizeLimit = 10
	buf.bufferSizeLimit = 100
	c.Assert(buf.Delete(Key("short")), IsNil)
	c.Assert(ErrEntryTooLarge.Equal(buf.Delete(Key("a key longer than ten"))), IsTrue)

	var err error
	for i := 0; i < 100 && err == nil; i++ {
		err = buf.Delete(Key([]byte{byte(i), 'k', 'e', 'y'}))
	}
	c.Assert(ErrTxnTooLarge.Equal(err), IsTrue)
}
