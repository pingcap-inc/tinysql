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

package ranger_test

import (
	"context"
	"fmt"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/expression"
	plannercore "github.com/pingcap/tidb/planner/core"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/util/ranger"
	"github.com/pingcap/tidb/util/testkit"
	"github.com/pingcap/tidb/util/testleak"
)

// With the cap reached, the eq/IN conditions past the kept prefix become
// filters instead of multiplying the ranges, so nothing is lost.
func (s *testRangerSuite) TestEqOrInRangeLimit(c *C) {
	defer testleak.AfterTest(c)()
	dom, store, err := newDomainStoreWithBootstrap(c)
	defer func() {
		dom.Close()
		store.Close()
	}()
	c.Assert(err, IsNil)
	testKit := testkit.NewTestKit(c, store)
	testKit.MustExec("use test")
	testKit.MustExec("drop table if exists t")
	testKit.MustExec("create table t(a int, b int, c int, index idx_abc(a, b, c))")

	sctx := testKit.Se.(session.Session)
	stmts, err := session.Parse(sctx, "select * from t where a = 1 and b in (1, 2, 3) and c in (4, 5, 6)")
	c.Assert(err, IsNil)
	is := domain.GetDomain(sctx).InfoSchema()
	c.Assert(plannercore.Preprocess(sctx, stmts[0], is), IsNil)
	p, _, err := plannercore.BuildLogicalPlan(context.Background(), sctx, stmts[0], is)
	c.Assert(err, IsNil)
	selection := p.(plannercore.LogicalPlan).Children()[0].(*plannercore.LogicalSelection)
	tbl := selection.Children()[0].(*plannercore.DataSource).TableInfo()
	conds := make([]expression.Expression, len(selection.Conditions))
	for i, cond := range selection.Conditions {
		conds[i] = expression.PushDownNot(sctx, cond)
	}
	cols, lengths := expression.IndexInfo2PrefixCols(tbl.Columns, selection.Schema().Columns, tbl.Indices[0])

	res, err := ranger.DetachCondAndBuildRangeForIndex(sctx, conds, cols, lengths)
	c.Assert(err, IsNil)
	c.Assert(res.Ranges, HasLen, 9)
	c.Assert(res.RemainedConds, HasLen, 0)

	defer func(old int) { ranger.MaxPointRanges = old }(ranger.MaxPointRanges)
	ranger.MaxPointRanges = 4
	res, err = ranger.DetachCondAndBuildRangeForIndex(sctx, conds, cols, lengths)
	c.Assert(err, IsNil)
	// a = 1 and b in (...) give 3 ranges; c in (...) would make 9, so it is a filter.
	c.Assert(res.Ranges, HasLen, 3)
	c.Assert(res.EqOrInCount, Equals, 2)
	c.Assert(fmt.Sprintf("%s", res.RemainedConds), Equals, "[in(test.t.c, 4, 5, 6)]")
}

// The ranges an eq/IN prefix builds are multiplied by the ranges of the next
// column and summed over the items of an OR; both are bounded as well.
func (s *testRangerSuite) TestRangeLimitCountsAllRanges(c *C) {
	defer testleak.AfterTest(c)()
	dom, store, err := newDomainStoreWithBootstrap(c)
	defer func() {
		dom.Close()
		store.Close()
	}()
	c.Assert(err, IsNil)
	testKit := testkit.NewTestKit(c, store)
	testKit.MustExec("use test")
	testKit.MustExec("drop table if exists t")
	testKit.MustExec("create table t(a int, b int, c int, index idx_ab(a, b))")
	sctx := testKit.Se.(session.Session)
	detach := func(where string) *ranger.DetachRangeResult {
		stmts, err := session.Parse(sctx, "select * from t where "+where)
		c.Assert(err, IsNil)
		is := domain.GetDomain(sctx).InfoSchema()
		c.Assert(plannercore.Preprocess(sctx, stmts[0], is), IsNil)
		p, _, err := plannercore.BuildLogicalPlan(context.Background(), sctx, stmts[0], is)
		c.Assert(err, IsNil)
		selection := p.(plannercore.LogicalPlan).Children()[0].(*plannercore.LogicalSelection)
		tbl := selection.Children()[0].(*plannercore.DataSource).TableInfo()
		conds := make([]expression.Expression, len(selection.Conditions))
		for i, cond := range selection.Conditions {
			conds[i] = expression.PushDownNot(sctx, cond)
		}
		cols, lengths := expression.IndexInfo2PrefixCols(tbl.Columns, selection.Schema().Columns, tbl.Indices[0])
		res, err := ranger.DetachCondAndBuildRangeForIndex(sctx, conds, cols, lengths)
		c.Assert(err, IsNil)
		return res
	}
	const inThenOr = "a in (1, 2, 3) and (b = 1 or b = 2 or b = 3)"
	const orOfIns = "(a = 1 and b in (1, 5, 9)) or (a = 2 and b in (2, 4, 6))"
	const longIn = "a in (1, 2, 3, 4, 5)"
	c.Assert(detach(inThenOr).Ranges, HasLen, 9)
	c.Assert(detach(orOfIns).Ranges, HasLen, 6)

	defer func(old int) { ranger.MaxPointRanges = old }(ranger.MaxPointRanges)
	ranger.MaxPointRanges = 4

	// 3 points on a times 3 ranges on b would be 9: the OR on b becomes a filter.
	res := detach(inThenOr)
	c.Assert(res.Ranges, HasLen, 3)
	c.Assert(fmt.Sprintf("%s", res.AccessConds), Equals, "[in(test.t.a, 1, 2, 3)]")
	c.Assert(fmt.Sprintf("%s", res.RemainedConds), Equals, "[or(eq(test.t.b, 1), or(eq(test.t.b, 2), eq(test.t.b, 3)))]")

	// Each item builds 3 ranges, 6 in all: the index is scanned whole and the
	// whole OR stays a filter.
	res = detach(orOfIns)
	c.Assert(fmt.Sprintf("%v", res.Ranges), Equals, "[[NULL,+inf]]")
	c.Assert(res.AccessConds, HasLen, 0)
	c.Assert(res.RemainedConds, HasLen, 1)

	// One IN list grows linearly with the statement, so it keeps the index.
	c.Assert(detach(longIn).Ranges, HasLen, 5)

	// The rows do not depend on the cap.
	testKit.MustExec("insert into t values (1, 1, 1), (1, 4, 2), (2, 5, 3), (2, 2, 4), (3, 3, 5), (4, 1, 6), (5, 9, 7)")
	for _, where := range []string{inThenOr, orOfIns, longIn} {
		q := "select c from t use index(idx_ab) where " + where + " order by c"
		ranger.MaxPointRanges = 100000
		want := testKit.MustQuery(q).Rows()
		ranger.MaxPointRanges = 1
		testKit.MustQuery(q).Check(want)
	}
}
