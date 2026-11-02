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

package core

import (
	"strings"

	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/mysql"
)

// checkSystemSchemaWrite refuses client statements that could change the
// mysql system schema. The server's own sessions (bootstrap, SET GLOBAL,
// statistics, DDL workers) run with InRestrictedSQL and may write there.
//
// It denies by default: only statement types that can't write a table pass
// unchecked. Any other statement is refused if it names a mysql table
// anywhere, including tables it only reads (INSERT ... SELECT FROM mysql.x),
// and CREATE/DROP DATABASE are refused for mysql itself.
func (p *preprocessor) checkSystemSchemaWrite(node ast.Node) {
	stmt, ok := node.(ast.StmtNode)
	if !ok {
		return
	}
	vars := p.ctx.GetSessionVars()
	if vars.InRestrictedSQL {
		return
	}
	user := vars.User
	if user == "" {
		user = "root"
	}
	var op string
	switch x := stmt.(type) {
	case *ast.SelectStmt, *ast.ShowStmt, *ast.ExplainStmt, *ast.UseStmt, *ast.SetStmt,
		*ast.BeginStmt, *ast.CommitStmt, *ast.RollbackStmt:
		// EXPLAIN's inner statement is checked when the preprocessor reaches
		// it; SET GLOBAL goes through SetGlobalSysVar, which validates and
		// logs the value.
		return
	case *ast.AdminStmt:
		if x.Tp == ast.AdminShowDDL || x.Tp == ast.AdminShowDDLJobs {
			return
		}
		op = "ADMIN"
	case *ast.CreateDatabaseStmt:
		p.checkSystemDB(x.Name, user)
		return
	case *ast.DropDatabaseStmt:
		p.checkSystemDB(x.Name, user)
		return
	case *ast.InsertStmt:
		op = "INSERT"
		if x.IsReplace {
			op = "REPLACE"
		}
	case *ast.DeleteStmt:
		op = "DELETE"
	case *ast.CreateTableStmt:
		op = "CREATE"
	case *ast.DropTableStmt, *ast.TruncateTableStmt:
		op = "DROP"
	case *ast.AlterTableStmt:
		op = "ALTER"
	case *ast.CreateIndexStmt, *ast.DropIndexStmt:
		op = "INDEX"
	case *ast.AnalyzeTableStmt:
		op = "ANALYZE"
	default:
		op = "WRITE"
	}
	var names tableNameCollector
	stmt.Accept(&names)
	for _, tn := range names.tables {
		schema := tn.Schema.L
		if schema == "" {
			schema = strings.ToLower(vars.CurrentDB)
		}
		if schema == mysql.SystemDB {
			p.err = ErrTableaccessDenied.GenWithStackByArgs(op, user, "%", tn.Name.O)
			return
		}
	}
}

func (p *preprocessor) checkSystemDB(name, user string) {
	if strings.EqualFold(name, mysql.SystemDB) {
		p.err = ErrDBaccessDenied.GenWithStackByArgs(user, "%", mysql.SystemDB)
	}
}

// tableNameCollector collects every table a statement names.
type tableNameCollector struct {
	tables []*ast.TableName
}

func (t *tableNameCollector) Enter(n ast.Node) (ast.Node, bool) {
	if tn, ok := n.(*ast.TableName); ok {
		t.tables = append(t.tables, tn)
	}
	return n, false
}

func (t *tableNameCollector) Leave(n ast.Node) (ast.Node, bool) {
	return n, true
}
