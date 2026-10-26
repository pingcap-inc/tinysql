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

package ddl

import (
	"strings"

	"github.com/pingcap/tidb/parser/model"
	"github.com/pingcap/tidb/util/sqlescape"
)

// nullCheckSQL returns the internal query checkForNullValue runs: whether any
// row has a NULL in one of cols. Names are quoted, since an identifier may
// itself contain a backtick.
func nullCheckSQL(schema, table model.CIStr, cols []*model.ColumnInfo) string {
	conds := make([]string, 0, len(cols))
	for _, col := range cols {
		conds = append(conds, sqlescape.Identifier(col.Name.L)+" is null")
	}
	return "select 1 from " + sqlescape.Identifier(schema.L) + "." + sqlescape.Identifier(table.L) +
		" where " + strings.Join(conds, " or ") + " limit 1;"
}
