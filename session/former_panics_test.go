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

package session_test

import (
	"context"
	"runtime/debug"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/store/mockstore"
	"github.com/pingcap/tidb/util/sqlexec"
	"github.com/pingcap/tidb/util/testkit"
)

// testFormerPanicsSuite has no per-test teardown: testSessionSuite drops every
// table after each test, and DROP TABLE of a table with rows hangs on the
// unfinished course code.
type testFormerPanicsSuite struct {
	store kv.Storage
	dom   *domain.Domain
}

var _ = Suite(&testFormerPanicsSuite{})

func (s *testFormerPanicsSuite) SetUpSuite(c *C) {
	var err error
	s.store, err = mockstore.NewMockTikvStore()
	c.Assert(err, IsNil)
	session.SetSchemaLease(0)
	session.DisableStats4Test()
	s.dom, err = session.BootstrapSession(s.store)
	c.Assert(err, IsNil)
}

func (s *testFormerPanicsSuite) TearDownSuite(c *C) {
	s.dom.Close()
	s.store.Close()
}

// Statements that used to panic a connection now fail cleanly or work.
func (s *testFormerPanicsSuite) TestFormerPanicsFailCleanly(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists fp_t, fp_d, fp_b, fp_u, fp_ok")
	tk.MustExec("create table fp_t (a int, b int)")
	tk.MustExec("insert into fp_t values (1, 1), (2, 2)")

	mustFail := func(sql string) {
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					c.Fatalf("panic for %q: %v\n%s", sql, r, debug.Stack())
				}
			}()
			var rs sqlexec.RecordSet
			rs, err = tk.Exec(sql)
			if err == nil && rs != nil {
				// Some errors only come when the rows are read.
				_, err = session.GetRows4Test(context.Background(), tk.Se, rs)
				rs.Close()
			}
		}()
		c.Assert(err, NotNil, Commentf("%s", sql))
	}
	// _tidb_rowid in INSERT ... SELECT, DELETE and DDL on information_schema,
	// TRUNCATE, EXPLAIN dot for DML, an expression index.
	mustFail("insert into fp_t (_tidb_rowid) select 1")
	mustFail("delete from information_schema.tables")
	mustFail("create table information_schema.fp_x (a int)")
	mustFail("truncate table fp_t")
	mustFail("explain format = 'dot' delete from fp_t")
	mustFail("create index fp_i on fp_t ((1))")
	mustFail("create table fp_f (a text, fulltext ((a)))")
	mustFail("create table fp_f (a int, fulltext key ((1)))")
	mustFail("alter table fp_t add fulltext ((1))")
	mustFail("drop database information_schema")
	// Types TinySQL does not evaluate.
	tk.MustExec("create table fp_d (d decimal(10, 2))")
	mustFail("insert into fp_d values (1.5)")
	mustFail("select max(d) from fp_d")
	mustFail("select ifnull(d, d) from fp_d")
	// Comparing DECIMAL with integer constants works (as reals): no error, no panic.
	tk.MustQuery("select * from fp_d where d in (1, 2)").Check(testkit.Rows())

	// The DDL queue still works after the refused DDL.
	tk.MustExec("create table fp_ok (a int)")
	// TopN with an offset above MaxInt64 returns no rows.
	tk.MustQuery("select a from fp_t order by a limit 9223372036854775808, 1").Check(testkit.Rows())
	// A BIT value could be stored but not read back (reading it crashed the
	// server), so writing one is refused and the table stays readable.
	tk.MustExec("create table fp_b (a bit(8), b int)")
	mustFail("insert into fp_b values (1, 1)")
	tk.MustQuery("select a, b from fp_b").Check(testkit.Rows())
	tk.MustQuery("select count(a) from fp_b")
	// A duplicate unique key is a normal error.
	tk.MustExec("create table fp_u (a int, unique key (a))")
	tk.MustExec("insert into fp_u values (1)")
	mustFail("insert into fp_u values (1)")
}
