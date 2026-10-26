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

// checkSystemSchemaWrite refuses writes (DML and table or index DDL) to tables
// in the mysql system schema from client sessions. The server's own sessions
// (bootstrap, SET GLOBAL, statistics, DDL workers) run with InRestrictedSQL
// and may write there. Reading the system tables stays allowed.
func (p *preprocessor) checkSystemSchemaWrite(node ast.Node) {
	vars := p.ctx.GetSessionVars()
	if vars.InRestrictedSQL {
		return
	}
	var (
		op     string
		tables []*ast.TableName
	)
	switch x := node.(type) {
	case *ast.InsertStmt:
		op = "INSERT"
		if x.IsReplace {
			op = "REPLACE"
		}
		if x.Table != nil {
			tables = tableNamesIn(x.Table.TableRefs)
		}
	case *ast.DeleteStmt:
		op = "DELETE"
		if x.TableRefs != nil {
			tables = tableNamesIn(x.TableRefs.TableRefs)
		}
	case *ast.CreateTableStmt:
		op, tables = "CREATE", []*ast.TableName{x.Table}
	case *ast.DropTableStmt:
		op, tables = "DROP", x.Tables
	case *ast.AlterTableStmt:
		op, tables = "ALTER", []*ast.TableName{x.Table}
	case *ast.CreateIndexStmt:
		op, tables = "INDEX", []*ast.TableName{x.Table}
	case *ast.DropIndexStmt:
		op, tables = "INDEX", []*ast.TableName{x.Table}
	case *ast.TruncateTableStmt:
		op, tables = "DROP", []*ast.TableName{x.Table}
	default:
		return
	}
	for _, tn := range tables {
		if tn == nil {
			continue
		}
		schema := tn.Schema.L
		if schema == "" {
			schema = strings.ToLower(vars.CurrentDB)
		}
		if schema == mysql.SystemDB {
			user := vars.User
			if user == "" {
				user = "root"
			}
			p.err = ErrTableaccessDenied.GenWithStackByArgs(op, user, "%", tn.Name.O)
			return
		}
	}
}

// tableNamesIn returns the tables named in a FROM-style clause.
func tableNamesIn(node ast.ResultSetNode) []*ast.TableName {
	switch x := node.(type) {
	case *ast.TableName:
		return []*ast.TableName{x}
	case *ast.TableSource:
		return tableNamesIn(x.Source)
	case *ast.Join:
		names := tableNamesIn(x.Left)
		if x.Right != nil {
			names = append(names, tableNamesIn(x.Right)...)
		}
		return names
	}
	return nil
}
