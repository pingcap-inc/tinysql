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

package session

import (
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser"
)

// DDL, ANALYZE, ADMIN and SET GLOBAL get an audit line naming their objects;
// reads, writes and session settings don't.
func (s *testMainSuite) TestAuditedStatements(c *C) {
	p := parser.New()
	for _, ca := range []struct {
		sql     string
		label   string
		objects []string
	}{
		{"create table t1 (a int)", "CreateTable", []string{"test.t1"}},
		{"drop table db2.t2, t3", "DropTable", []string{"db2.t2", "test.t3"}},
		{"alter table t1 add index i (a)", "AlterTable", []string{"test.t1"}},
		{"create index i on t1 (a)", "CreateIndex", []string{"test.t1"}},
		{"truncate table t1", "TruncateTable", []string{"test.t1"}},
		{"create database d1", "CreateDatabase", []string{"d1"}},
		{"drop database d1", "DropDatabase", []string{"d1"}},
		{"analyze table t1", "AnalyzeTable", []string{"test.t1"}},
		{"admin show ddl jobs", "Admin", []string{}},
		{"set global sql_mode = '', @@session.autocommit = 1", "Set", []string{"@@global.sql_mode"}},
	} {
		node, err := p.ParseOneStmt(ca.sql, "", "")
		c.Assert(err, IsNil)
		c.Assert(isAdminStatement(node), IsTrue, Commentf(ca.sql))
		c.Assert(stmtLabel(node), Equals, ca.label)
		c.Assert(auditObjects(node, "test"), DeepEquals, ca.objects, Commentf(ca.sql))
	}
	for _, sql := range []string{
		"select * from t1",
		"insert into t1 values (1)",
		"delete from t1",
		"set autocommit = 0",
		"set @@session.sql_mode = ''",
		"use test",
		"begin",
	} {
		node, err := p.ParseOneStmt(sql, "", "")
		c.Assert(err, IsNil)
		c.Assert(isAdminStatement(node), IsFalse, Commentf(sql))
	}
}
