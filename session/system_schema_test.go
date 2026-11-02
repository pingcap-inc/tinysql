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
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/store/mockstore"
	"github.com/pingcap/tidb/util/testkit"
)

// testSystemSchemaSuite has no per-test teardown, for the same reason as
// testFormerPanicsSuite.
type testSystemSchemaSuite struct {
	store kv.Storage
	dom   *domain.Domain
}

var _ = Suite(&testSystemSchemaSuite{})

func (s *testSystemSchemaSuite) SetUpSuite(c *C) {
	var err error
	s.store, err = mockstore.NewMockTikvStore()
	c.Assert(err, IsNil)
	session.SetSchemaLease(0)
	session.DisableStats4Test()
	s.dom, err = session.BootstrapSession(s.store)
	c.Assert(err, IsNil)
}

func (s *testSystemSchemaSuite) TearDownSuite(c *C) {
	s.dom.Close()
	s.store.Close()
}

// The mysql schema guard denies by default: any statement that isn't
// read-only is refused if it names a mysql table, however it names it.
func (s *testSystemSchemaSuite) TestSystemSchemaGuardDeniesByDefault(c *C) {
	tk := testkit.NewTestKitWithInit(c, s.store)
	tk.MustExec("drop table if exists sst_t")
	tk.MustExec("create table sst_t (a int)")
	for _, sql := range []string{
		"analyze table mysql.stats_meta",
		"analyze table test.sst_t, mysql.stats_buckets",
		"create table sst_like like mysql.tidb",
		"insert into sst_t select count from mysql.stats_meta",
		"explain insert into mysql.tidb values ('x', 'y', 'z')",
	} {
		_, err := tk.Exec(sql)
		c.Assert(err, NotNil, Commentf("%s", sql))
		c.Assert(err.Error(), Matches, ".*command denied.*", Commentf("%s", sql))
	}
	for _, sql := range []string{"create database mysql", "create database if not exists MySQL", "drop database mysql"} {
		_, err := tk.Exec(sql)
		c.Assert(err, NotNil, Commentf("%s", sql))
		c.Assert(err.Error(), Matches, ".*Access denied.*database 'mysql'.*", Commentf("%s", sql))
	}
	tk.MustExec("use mysql")
	_, err := tk.Exec("analyze table stats_meta")
	c.Assert(err, NotNil)

	// Reads and session statements still work.
	tk.MustQuery("select variable_name from mysql.global_variables where variable_name = 'sql_mode'").Check(testkit.Rows("sql_mode"))
	tk.MustExec("explain select * from mysql.tidb")
	tk.MustExec("show tables")
	tk.MustExec("admin show ddl jobs")
	tk.MustExec("use test")
	tk.MustExec("begin")
	tk.MustExec("insert into sst_t values (1)")
	tk.MustExec("commit")
	tk.MustExec("set global sql_mode = ''")
	tk.MustExec("set @@session.sql_mode = ''")
}
