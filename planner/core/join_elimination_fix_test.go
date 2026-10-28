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
	"context"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/expression"
	"github.com/pingcap/tidb/infoschema"
	"github.com/pingcap/tidb/parser"
	"github.com/pingcap/tidb/parser/model"
)

// An outer join may only be dropped when its keys cover a whole unique index
// of the inner table. Before the fix the check read IdxCols, which is empty
// during logical optimization, so any unique index looked covered.
type testJoinEliminationFixSuite struct{}

var _ = Suite(&testJoinEliminationFixSuite{})

func (s *testJoinEliminationFixSuite) TestOuterJoinNeedsWholeUniqueIndex(c *C) {
	is := infoschema.MockInfoSchema([]*model.TableInfo{MockSignedTable()})
	// The skeleton's grammar has no JOIN yet (proj2), so check the
	// uniqueness test on the inner side directly.
	stmt, err := parser.New().ParseOneStmt("select * from t", "", "")
	c.Assert(err, IsNil)
	ctx := MockContext()
	c.Assert(Preprocess(ctx, stmt, is), IsNil)
	builder := NewPlanBuilder(ctx, is)
	p, err := builder.Build(context.TODO(), stmt)
	c.Assert(err, IsNil)
	lp := p.(LogicalPlan)
	for len(lp.Children()) > 0 {
		lp = lp.Children()[0]
	}
	ds := lp.(*DataSource)
	keys := func(names ...string) *expression.Schema {
		var cols []*expression.Column
		for _, name := range names {
			for i, col := range ds.Columns {
				if col.Name.L == name {
					cols = append(cols, ds.schema.Columns[i])
				}
			}
		}
		return expression.NewSchema(cols...)
	}
	o := &outerJoinEliminator{}
	for _, tt := range []struct {
		keys   []string
		unique bool
	}{
		{[]string{"c"}, false},          // only the first column of unique (c, d, e)
		{[]string{"c", "d", "e"}, true}, // the whole of unique (c, d, e)
		{[]string{"f"}, true},           // unique (f)
		{[]string{"e"}, false},          // unique (e) is write-only, not usable
		{[]string{"g"}, false},          // g is not unique on its own
	} {
		got, err := o.isInnerJoinKeysContainIndex(ds, keys(tt.keys...))
		c.Assert(err, IsNil)
		c.Assert(got, Equals, tt.unique, Commentf("join keys %v", tt.keys))
	}
}
