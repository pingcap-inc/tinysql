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

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/parser/model"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/store/mockstore"
	"github.com/pingcap/tidb/util/testkit"
)

// testEngineFixesSuite has no per-test teardown, for the same reason as
// testFormerPanicsSuite. Results are checked through projections and
// pushed-down filters, never through count(*) or a root Selection, which
// are course assignments.
type testEngineFixesSuite struct {
	store kv.Storage
	dom   *domain.Domain
}

var _ = Suite(&testEngineFixesSuite{})

func (s *testEngineFixesSuite) SetUpSuite(c *C) {
	var err error
	s.store, err = mockstore.NewMockTikvStore()
	c.Assert(err, IsNil)
	session.SetSchemaLease(0)
	session.DisableStats4Test()
	s.dom, err = session.BootstrapSession(s.store)
	c.Assert(err, IsNil)
}

func (s *testEngineFixesSuite) TearDownSuite(c *C) {
	s.dom.Close()
	s.store.Close()
}

// queryErr runs sql and reads its rows, and returns the error from either.
func queryErr(tk *testkit.TestKit, sql string) error {
	rs, err := tk.Exec(sql)
	if err != nil || rs == nil {
		return err
	}
	defer rs.Close()
	_, err = session.GetRows4Test(context.Background(), tk.Se, rs)
	return err
}

// Hex and bit literals compare by their numeric value instead of as 0.
func (s *testEngineFixesSuite) TestHexLiteralComparesByValue(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists ef_hex")
	tk.MustExec("create table ef_hex (a int)")
	tk.MustExec("insert into ef_hex values (65), (66), (0)")
	tk.MustQuery("select a, a = 0x41, a = b'1000001' from ef_hex").Sort().Check(testkit.Rows("0 0 0", "65 1 1", "66 0 0"))
	tk.MustQuery("select 0x41 + 0, 0x41 = 65, b'11' + 0").Check(testkit.Rows("65 1 3"))
}

// Statements that used to panic in planning or execution now return errors.
func (s *testEngineFixesSuite) TestUnsupportedTypesFailCleanly(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists ef_t, ef_dec, ef_date")
	tk.MustExec("create table ef_t (a int)")
	tk.MustExec("insert into ef_t values (1)")
	tk.MustExec("create table ef_dec (a int, b decimal(10, 2))")
	tk.MustExec("create table ef_date (c date)")
	for _, sql := range []string{
		// VALUES() over a type it can't evaluate.
		"select values(b) from ef_dec",
		// VALUES() whose column the HAVING/ORDER BY resolver replaced.
		"select 1 as c from ef_t having sum(values(c)) > 0",
		"select 1 as c from ef_t order by sum(values(c))",
		// ORDER BY a type the executor can't compare, as a sort and a TopN.
		"select * from ef_dec order by b",
		"select * from ef_dec order by b limit 1",
	} {
		c.Assert(queryErr(tk, sql), NotNil, Commentf("%s", sql))
	}
	// A DATE equi-join no longer panics building the not-null filter.
	tk.MustQuery("select * from ef_date x, ef_date y where x.c = y.c").Check(testkit.Rows())

	// An unsupported conversion stays an error in non-strict mode instead of
	// becoming a warning and a stored zero.
	tk.MustExec("set sql_mode = ''")
	tk.MustGetErrCode("insert into ef_dec values (1, 1.5)", 1235)
	tk.MustQuery("select a from ef_dec").Check(testkit.Rows())
}

// A CHECK constraint is ignored instead of becoming an index with no columns,
// and a user column can't take the hidden handle's name.
func (s *testEngineFixesSuite) TestDDLSchemaFixes(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists ef_chk, ef_rowid")
	tk.MustExec("create table ef_chk (a int, check (a > 0))")
	is := s.dom.InfoSchema()
	tbl, err := is.TableByName(model.NewCIStr("test"), model.NewCIStr("ef_chk"))
	c.Assert(err, IsNil)
	c.Assert(tbl.Meta().Indices, HasLen, 0)

	for _, sql := range []string{
		"create table ef_rowid (a int, _tidb_rowid int)",
		"create table ef_rowid (a int, _TiDB_RowID int)",
	} {
		tk.MustGetErrCode(sql, 1166)
	}
	tk.MustExec("create table ef_rowid (a int)")
	for _, sql := range []string{
		"alter table ef_rowid add column _tidb_rowid int",
		"alter table ef_rowid change a _tidb_rowid int",
		"alter table ef_rowid modify column _tidb_rowid int",
	} {
		tk.MustGetErrCode(sql, 1166)
	}
}

// The cascades planner keeps 'not (a = x)' on an index scan.
func (s *testEngineFixesSuite) TestCascadesKeepsNegatedIndexCondition(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists ef_casc")
	tk.MustExec("create table ef_casc (a int, b int, index ia(a))")
	tk.MustExec("insert into ef_casc values (1, 1), (2, 2), (3, 3)")
	tk.MustExec("set session tidb_enable_cascades_planner = 1")
	defer tk.MustExec("set session tidb_enable_cascades_planner = 0")
	tk.MustQuery("select a from ef_casc use index(ia) where not (a = 2)").Sort().Check(testkit.Rows("1", "3"))
	tk.MustQuery("select a from ef_casc use index(ia) where not (a != 2)").Check(testkit.Rows("2"))
}
