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

package parser_test

import (
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser"
	"github.com/pingcap/tidb/parser/ast"
)

var _ = Suite(&testCrashFixesSuite{})

type testCrashFixesSuite struct{}

// Inputs that used to panic the parser. (This is an external test package
// because package parser defines a token named recover.) Each must now parse or fail cleanly.
func (s *testCrashFixesSuite) TestNoPanic(c *C) {
	p := parser.New()
	for _, sql := range []string{
		// Special-comment offsets: no space after the prefix, a long word, newlines.
		"select /*!40001SQL_NO_CACHE*/ a, b from t",
		"select /*!SQL_NO_CACHE_AND_A_VERY_LONG_IDENTIFIER_WITHOUT_ANY_SPACE*/ a, b from t",
		"select /*!40101\n\n\n a */, b from t",
		"select /*T!40000 a */, b from t",
		"select /*+HINT(t1)*/ a from t",
		// A version number that is not right after "/*T!" (used to recurse
		// until the stack overflowed), and a 4-digit /*! version.
		"select /*T![x] 00001 */ 1",
		"select /*T!a 40000 */ 1",
		"select /*!1234 1 */",
		// Integer literals above MaxInt64.
		"admin show ddl jobs 9223372036854775808",
		"select /*+ MEMORY_QUOTA(18446744073709551615 MB) */ 1",
		// Column lengths above MaxUint32.
		"create table t (a varchar(9223372036854775808))",
		"create table t (a decimal(18446744073709551615, 2))",
	} {
		func() {
			defer func() {
				r := recover()
				c.Assert(r, IsNil, Commentf("panic for %q: %v", sql, r))
			}()
			_, _, _ = p.Parse(sql, "", "")
		}()
	}
}

func (s *testCrashFixesSuite) TestOutOfRangeLiterals(c *C) {
	p := parser.New()
	_, _, err := p.Parse("admin show ddl jobs 9223372036854775808", "", "")
	c.Assert(err, NotNil)
	_, _, err = p.Parse("create table t (a varchar(9223372036854775808))", "", "")
	c.Assert(err, NotNil)
	_, _, err = p.Parse("create table t (a decimal(18446744073709551615, 2))", "", "")
	c.Assert(err, NotNil)
	_, _, err = p.Parse("create table t (a decimal(10, 18446744073709551615))", "", "")
	c.Assert(err, ErrorMatches, ".*Too big scale.*")

	stmts, _, err := p.Parse("select /*+ MEMORY_QUOTA(18446744073709551615 MB) */ 1", "", "")
	c.Assert(err, IsNil)
	hints := stmts[0].(*ast.SelectStmt).TableHints
	c.Assert(hints, HasLen, 1)
	c.Assert(hints[0].MemoryQuota, Equals, int64(-1))

	stmts, _, err = p.Parse("select /*+ MEMORY_QUOTA(1 GB) */ 1", "", "")
	c.Assert(err, IsNil)
	c.Assert(stmts[0].(*ast.SelectStmt).TableHints[0].MemoryQuota, Equals, int64(1024*1024*1024))
}
