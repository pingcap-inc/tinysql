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
	"encoding/binary"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/sessionctx/stmtctx"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/chunk"
	"github.com/pingcap/tidb/util/rowcodec"
)

var _ = Suite(&testMalformedSuite{})

// Row values come from the store. Counts, offsets and lengths that don't fit
// the data must be errors, not slices past the end.
type testMalformedSuite struct{}

func (s *testMalformedSuite) TestMalformedRowsAreRejected(c *C) {
	intTp := types.NewFieldType(mysql.TypeLonglong)
	strTp := types.NewFieldType(mysql.TypeVarchar)
	cols := []rowcodec.ColInfo{
		{ID: 1, Tp: int32(intTp.Tp), Flag: int32(intTp.Flag)},
		{ID: 2, Tp: int32(strTp.Tp), Flag: int32(strTp.Flag)},
	}
	fts := []*types.FieldType{intTp, strTp}

	var encoder rowcodec.Encoder
	sc := &stmtctx.StatementContext{TimeZone: time.UTC}
	good, err := encoder.Encode(sc, []int64{1, 2}, []types.Datum{types.NewIntDatum(1), types.NewStringDatum("abc")}, nil)
	c.Assert(err, IsNil)
	c.Assert(s.decode(cols, fts, good), IsNil)

	// Small format: version, flag, 2 not-null and 0 null columns, 2 one-byte
	// column IDs at 6, then 2 two-byte end offsets at 8 and 10.
	withOffsets := func(first, second uint16) []byte {
		b := append([]byte(nil), good...)
		binary.LittleEndian.PutUint16(b[8:], first)
		binary.LittleEndian.PutUint16(b[10:], second)
		return b
	}
	tooManyCols := append([]byte(nil), good...)
	binary.LittleEndian.PutUint16(tooManyCols[2:], 200)

	bad := map[string][]byte{
		"version only":      {rowcodec.CodecVer},
		"truncated header":  good[:5],
		"truncated offsets": good[:9],
		"too many columns":  tooManyCols,
		"offset past data":  withOffsets(1, 0xffff),
		"offsets descend":   withOffsets(1, 0),
		// Large format: 4-byte column IDs and offsets that aren't there.
		"large truncated": {rowcodec.CodecVer, 1, 1, 0, 0, 0, 1, 0, 0},
		// One int column whose value is 3 bytes long.
		"3-byte int": {rowcodec.CodecVer, 0, 1, 0, 0, 0, 1, 3, 0, 1, 2, 3},
	}
	for name, b := range bad {
		c.Assert(s.decode(cols, fts, b), NotNil, Commentf(name))
	}

	// A BIT value is an encoded uint too.
	bitTp := types.NewFieldType(mysql.TypeBit)
	bitTp.Flen = 8
	bitCols := []rowcodec.ColInfo{{ID: 1, Tp: int32(bitTp.Tp), Flen: bitTp.Flen}}
	threeByteBit := []byte{rowcodec.CodecVer, 0, 1, 0, 0, 0, 1, 3, 0, 1, 2, 3}
	_, err = rowcodec.NewDatumMapDecoder(bitCols, -1, time.UTC).DecodeToDatumMap(threeByteBit, 0, nil)
	c.Assert(err, NotNil)
	err = rowcodec.NewChunkDecoder(bitCols, -1, nil, time.UTC).DecodeToChunk(threeByteBit, 0, chunk.New([]*types.FieldType{bitTp}, 1, 1))
	c.Assert(err, NotNil)
}

// decode runs every decoder over b and returns the first error.
func (s *testMalformedSuite) decode(cols []rowcodec.ColInfo, fts []*types.FieldType, b []byte) error {
	if _, err := rowcodec.NewDatumMapDecoder(cols, -1, time.UTC).DecodeToDatumMap(b, 0, nil); err == nil {
		return nil
	} else if err := rowcodec.NewChunkDecoder(cols, -1, nil, time.UTC).DecodeToChunk(b, 0, chunk.New(fts, 1, 1)); err == nil {
		return nil
	} else if _, err := rowcodec.NewByteDecoder(cols, -1, nil, nil).DecodeToBytes(map[int64]int{1: 0, 2: 1}, 0, b, nil); err == nil {
		return nil
	} else {
		return err
	}
}
