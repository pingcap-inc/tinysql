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

package executor_test

import (
	"math"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/expression"
	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/mock"
)

// The vectorized and row-at-a-time paths must give the same answer. The tests
// live here, not in package expression, because make test-proj5-1 runs that
// whole package.
type testVecConsistencySuite struct{}

var _ = Suite(&testVecConsistencySuite{})

// eval runs expr over input with vectorized evaluation on or off and returns
// the output chunk.
func evalBothWays(c *C, expr expression.Expression, input *chunk.Chunk) (rowOut, vecOut *chunk.Chunk, rowErr, vecErr error) {
	run := func(vec bool) (*chunk.Chunk, error) {
		ctx := mock.NewContext()
		ctx.GetSessionVars().EnableVectorizedExpression = vec
		out := chunk.NewChunkWithCapacity([]*types.FieldType{expr.GetType()}, input.NumRows())
		err := expression.NewEvaluatorSuite([]expression.Expression{expr}).Run(ctx, input, out)
		return out, err
	}
	rowOut, rowErr = run(false)
	vecOut, vecErr = run(true)
	return
}

func (s *testVecConsistencySuite) TestUnsignedArithmeticOverflow(c *C) {
	signed := types.NewFieldType(mysql.TypeLonglong)
	unsigned := types.NewFieldType(mysql.TypeLonglong)
	unsigned.Flag |= mysql.UnsignedFlag
	ctx := mock.NewContext()

	// signed 0 minus unsigned 5 is negative: an overflow for BIGINT UNSIGNED.
	a := &expression.Column{RetType: signed, Index: 0}
	b := &expression.Column{RetType: unsigned, Index: 1}
	minus, err := expression.NewFunction(ctx, ast.Minus, types.NewFieldType(mysql.TypeLonglong), a, b)
	c.Assert(err, IsNil)
	input := chunk.NewChunkWithCapacity([]*types.FieldType{signed, unsigned}, 1)
	input.AppendInt64(0, 0)
	input.AppendUint64(1, 5)
	_, _, rowErr, vecErr := evalBothWays(c, minus, input)
	c.Assert(rowErr, NotNil)
	c.Assert(vecErr, NotNil)

	// unsigned MaxUint64-10 plus signed 1 fits: no overflow on either path.
	u := &expression.Column{RetType: unsigned, Index: 0}
	sg := &expression.Column{RetType: signed, Index: 1}
	plus, err := expression.NewFunction(ctx, ast.Plus, types.NewFieldType(mysql.TypeLonglong), u, sg)
	c.Assert(err, IsNil)
	input = chunk.NewChunkWithCapacity([]*types.FieldType{unsigned, signed}, 1)
	input.AppendUint64(0, math.MaxUint64-10)
	input.AppendInt64(1, 1)
	rowOut, vecOut, rowErr, vecErr := evalBothWays(c, plus, input)
	c.Assert(rowErr, IsNil)
	c.Assert(vecErr, IsNil)
	c.Assert(vecOut.GetRow(0).GetUint64(0), Equals, rowOut.GetRow(0).GetUint64(0))
	c.Assert(vecOut.GetRow(0).GetUint64(0), Equals, uint64(math.MaxUint64-9))

	// signed 0 minus signed MinInt64 overflows: -MinInt64 is MinInt64 again.
	x := &expression.Column{RetType: signed, Index: 0}
	y := &expression.Column{RetType: signed, Index: 1}
	minus, err = expression.NewFunction(ctx, ast.Minus, types.NewFieldType(mysql.TypeLonglong), x, y)
	c.Assert(err, IsNil)
	input = chunk.NewChunkWithCapacity([]*types.FieldType{signed, signed}, 1)
	input.AppendInt64(0, 0)
	input.AppendInt64(1, math.MinInt64)
	_, _, rowErr, vecErr = evalBothWays(c, minus, input)
	c.Assert(rowErr, NotNil)
	c.Assert(vecErr, NotNil)
}

func (s *testVecConsistencySuite) TestBitProjection(c *C) {
	bit := types.NewFieldType(mysql.TypeBit)
	bit.Flen = 8
	ctx := mock.NewContext()
	a := &expression.Column{RetType: bit, Index: 0}
	b := &expression.Column{RetType: bit, Index: 1}
	ifnull, err := expression.NewFunction(ctx, ast.Ifnull, bit, a, b)
	c.Assert(err, IsNil)
	c.Assert(ifnull.GetType().Tp, Equals, mysql.TypeBit)

	input := chunk.NewChunkWithCapacity([]*types.FieldType{bit, bit}, 2)
	for _, v := range []uint64{3, 200} {
		bv := types.NewBinaryLiteralFromUint(v, -1)
		input.AppendBytes(0, bv)
		input.AppendBytes(1, bv)
	}
	rowOut, vecOut, rowErr, vecErr := evalBothWays(c, ifnull, input)
	c.Assert(rowErr, IsNil)
	c.Assert(vecErr, IsNil)
	for i := 0; i < 2; i++ {
		// BIT values read as NULL in integer context on both paths; what
		// matters is that the vectorized path no longer panics.
		c.Assert(vecOut.GetRow(i).IsNull(0), Equals, rowOut.GetRow(i).IsNull(0))
		c.Assert(string(vecOut.GetRow(i).GetBytes(0)), Equals, string(rowOut.GetRow(i).GetBytes(0)))
	}
}

// Without an implicit cast, s + 1 asks for the float values of a string
// column. The vectorized path read the string data as floats out of bounds
// (and could crash the server); now it is an error.
func (s *testVecConsistencySuite) TestColumnReadAsOtherType(c *C) {
	str := types.NewFieldType(mysql.TypeVarchar)
	ctx := mock.NewContext()
	ctx.GetSessionVars().EnableVectorizedExpression = true
	col := &expression.Column{RetType: str, Index: 0}
	plus, err := expression.NewFunction(ctx, ast.Plus, types.NewFieldType(mysql.TypeDouble), col, expression.One.Clone())
	c.Assert(err, IsNil)
	input := chunk.NewChunkWithCapacity([]*types.FieldType{str}, 3000)
	for i := 0; i < 3000; i++ {
		input.AppendString(0, "")
	}
	out := chunk.NewChunkWithCapacity([]*types.FieldType{plus.GetType()}, input.NumRows())
	err = expression.NewEvaluatorSuite([]expression.Expression{plus}).Run(ctx, input, out)
	c.Assert(err, ErrorMatches, ".*does not convert it implicitly.*")
}
