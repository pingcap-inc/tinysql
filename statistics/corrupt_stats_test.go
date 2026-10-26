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

package statistics

import (
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tipb/go-tipb"
)

var _ = Suite(&testCorruptStatsSuite{})

type testCorruptStatsSuite struct{}

func (s *testCorruptStatsSuite) TestRaggedCMSketchIsRejected(c *C) {
	for _, rows := range [][]*tipb.CMSketchRow{
		{{Counters: []uint32{1}}, {Counters: []uint32{1, 2, 3}}},
		{{Counters: []uint32{}}, {Counters: []uint32{}}},
	} {
		p := &tipb.CMSketch{Rows: rows}
		data, err := p.Marshal()
		c.Assert(err, IsNil)
		_, err = DecodeCMSketch(data)
		c.Assert(err, NotNil)
		c.Assert(CMSketchFromProto(p), IsNil)
	}
	good := &tipb.CMSketch{Rows: []*tipb.CMSketchRow{{Counters: []uint32{1, 2}}, {Counters: []uint32{3, 4}}}}
	c.Assert(CMSketchFromProto(good), NotNil)
}

func (s *testCorruptStatsSuite) TestCalcFractionWithoutScalars(c *C) {
	// Integer bounds build no scalars; a string value must not index them.
	hg := NewHistogram(0, 1, 0, 0, types.NewFieldType(mysql.TypeLonglong), 1, 0)
	lower, upper := types.NewIntDatum(1), types.NewIntDatum(10)
	hg.AppendBucket(&lower, &upper, 10, 1)
	hg.PreCalculateScalar()
	value := types.NewStringDatum("abc")
	c.Assert(hg.calcFraction(0, &value), Equals, 0.5)
}
