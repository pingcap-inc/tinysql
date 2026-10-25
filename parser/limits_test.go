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

package parser

import (
	"strings"

	. "github.com/pingcap/check"
)

var _ = Suite(&testLimitsSuite{})

type testLimitsSuite struct{}

// Millions of comments used to recurse once each in the lexer and overflow
// the goroutine stack, which kills the process.
func (s *testLimitsSuite) TestManyCommentsDoNotRecurse(c *C) {
	p := New()
	for _, comment := range []string{"#\n", "-- \n", "/**/ "} {
		sql := strings.Repeat(comment, 2000000) + "select 1"
		stmts, _, err := p.Parse(sql, "", "")
		c.Assert(err, IsNil)
		c.Assert(stmts, HasLen, 1)
	}
}

func (s *testLimitsSuite) TestNestingDepthLimit(c *C) {
	p := New()
	_, _, err := p.Parse("select "+strings.Repeat("!", 100)+"1", "", "")
	c.Assert(err, IsNil)
	_, _, err = p.Parse("select "+strings.Repeat("!", MaxNestingDepth+10)+"1", "", "")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*nesting is too deep.*")
}
