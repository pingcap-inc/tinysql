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

// ADMIN's WHERE and a table CHECK constraint were skipped by the depth check,
// and the planner recurses once per level of the WHERE.
func (s *testLimitsSuite) TestNestingDepthLimitCoversAllClauses(c *C) {
	p := New()
	deep := strings.Repeat("!", MaxNestingDepth+10) + "1"
	for _, sql := range []string{
		"admin show ddl jobs 1 where " + deep,
		"create table t (a int, check (" + deep + "))",
	} {
		_, _, err := p.Parse(sql, "", "")
		c.Assert(err, NotNil, Commentf("%.40s", sql))
		c.Assert(err.Error(), Matches, ".*nesting is too deep.*")
	}
}

// The parser's stack is capped, so a huge statement is refused while it is
// parsed, and the grown stack isn't kept after the statement.
func (s *testLimitsSuite) TestParserStackIsBounded(c *C) {
	p := New()
	_, _, err := p.Parse("select "+strings.Repeat("!", 1000000)+"1", "", "")
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Matches, ".*nesting is too deep.*")
	c.Assert(len(p.cache), Equals, initialParserStack)
	c.Assert(p.yyVAL, IsNil)

	_, _, err = p.Parse("select "+strings.Repeat("!", 1000)+"1", "", "")
	c.Assert(err, IsNil)
	c.Assert(len(p.cache), Equals, initialParserStack)
}

// Statements the depth check accepts must still fit in the stack, including
// constructs that take several stack slots per level.
func (s *testLimitsSuite) TestParserStackFitsAcceptedDepth(c *C) {
	p := New()
	n := MaxNestingDepth - 10
	for _, open := range []string{"!", "abs(", "1 in (", "concat(1,", "1 not in (1,", "substring(1,1,"} {
		closing := ""
		if strings.HasSuffix(open, "(") || strings.HasSuffix(open, ",") {
			closing = strings.Repeat(")", n)
		}
		sql := "select " + strings.Repeat(open, n) + "1" + closing
		_, _, err := p.Parse(sql, "", "")
		c.Assert(err, IsNil, Commentf("%q nested %d deep", open, n))
	}
	// Long flat lists stay shallow.
	for _, sql := range []string{
		"select 1 in (" + strings.Repeat("1,", 100000) + "1)",
		"insert into t values " + strings.Repeat("(1),", 100000) + "(1)",
		"select a from t where " + strings.Repeat("a = 1 or ", 4990) + "a = 1",
	} {
		_, _, err := p.Parse(sql, "", "")
		c.Assert(err, IsNil, Commentf("%.40s", sql))
	}
}
