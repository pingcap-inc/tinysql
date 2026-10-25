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

package variable

import (
	"strconv"

	. "github.com/pingcap/check"
)

// Values that size goroutines, channels and chunks are bounded.
func (s *testVarsutilSuite) TestResourceVariableBounds(c *C) {
	v := NewSessionVars()
	for _, name := range []string{TiDBHashJoinConcurrency, TiDBHashAggPartialConcurrency, TiDBHashAggFinalConcurrency,
		TiDBIndexLookupConcurrency, TiDBIndexLookupJoinConcurrency, TiDBDistSQLScanConcurrency, TiDBDDLReorgWorkerCount,
		TiDBBuildStatsConcurrency} {
		val, err := ValidateSetSystemVar(v, name, "1000000000")
		c.Assert(err, IsNil, Commentf("%s", name))
		c.Assert(val, Equals, strconv.Itoa(maxConcurrency), Commentf("%s", name))
		_, err = ValidateSetSystemVar(v, name, "0")
		c.Assert(err, NotNil, Commentf("%s", name))
		_, err = ValidateSetSystemVar(v, name, "-1")
		c.Assert(err, NotNil, Commentf("%s", name))
	}
	val, err := ValidateSetSystemVar(v, TiDBProjectionConcurrency, "1000000000")
	c.Assert(err, IsNil)
	c.Assert(val, Equals, strconv.Itoa(maxConcurrency))
	val, err = ValidateSetSystemVar(v, TiDBProjectionConcurrency, "0")
	c.Assert(err, IsNil)
	c.Assert(val, Equals, "0")

	_, err = ValidateSetSystemVar(v, TiDBMaxChunkSize, strconv.Itoa(maxChunkSizeUpperBound+1))
	c.Assert(err, NotNil)
	_, err = ValidateSetSystemVar(v, TiDBMaxChunkSize, strconv.Itoa(maxChunkSizeUpperBound))
	c.Assert(err, IsNil)
}
