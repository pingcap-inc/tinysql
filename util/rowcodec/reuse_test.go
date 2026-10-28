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

package rowcodec_test

import (
	"strings"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/sessionctx/stmtctx"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/rowcodec"
)

// A session reuses one Encoder for every row. A large row followed by small
// ones must not leave stale offsets behind.
func (s *testSuite) TestEncoderReuseAfterLargeRow(c *C) {
	var encoder rowcodec.Encoder
	sc := &stmtctx.StatementContext{TimeZone: time.UTC}
	strType := types.NewFieldType(mysql.TypeVarchar)
	intType := types.NewFieldType(mysql.TypeLonglong)
	cols := []rowcodec.ColInfo{
		{ID: 1, Tp: int32(strType.Tp), Flen: strType.Flen},
		{ID: 2, Tp: int32(intType.Tp), Flen: intType.Flen},
	}
	decoder := rowcodec.NewDatumMapDecoder(cols, -1, sc.TimeZone)

	big := strings.Repeat("x", 70000)
	_, err := encoder.Encode(sc, []int64{1, 2}, []types.Datum{types.NewStringDatum(big), types.NewIntDatum(1)}, nil)
	c.Assert(err, IsNil)

	for i := int64(0); i < 3; i++ {
		row, err := encoder.Encode(sc, []int64{1, 2}, []types.Datum{types.NewStringDatum("ab"), types.NewIntDatum(i)}, nil)
		c.Assert(err, IsNil)
		dm, err := decoder.DecodeToDatumMap(row, -1, nil)
		c.Assert(err, IsNil)
		d1, d2 := dm[1], dm[2]
		c.Assert(d1.GetString(), Equals, "ab")
		c.Assert(d2.GetInt64(), Equals, i)
	}
}
