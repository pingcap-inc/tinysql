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

package codec

import (
	"math"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/sessionctx/stmtctx"
	"github.com/pingcap/tidb/types"
	"github.com/pingcap/tidb/util/chunk"
)

var _ = Suite(&testStoreBytesSuite{})

// Values decoded here come from the store. A malformed or mislabelled value
// must be an error, not a write through the wrong element size or a slice
// past the end.
type testStoreBytesSuite struct{}

func (s *testStoreBytesSuite) TestDecodeOneRejectsMismatchedFlag(c *C) {
	sc := &stmtctx.StatementContext{TimeZone: time.UTC}
	cases := []struct {
		value interface{}
		tp    byte
	}{
		{int64(-1), mysql.TypeFloat},     // 8 bytes into a 4-byte element
		{"abc", mysql.TypeLonglong},      // 3 bytes read as 8
		{int64(1), mysql.TypeVarchar},    // fixed-size write into a var-length column
		{uint64(1), mysql.TypeBlob},      // same, unsigned
		{float64(1.5), mysql.TypeLong},   // float into an int column
		{[]byte("abc"), mysql.TypeFloat}, // bytes into a fixed-length column
	}
	for _, ca := range cases {
		ft := types.NewFieldType(ca.tp)
		for _, comparable := range []bool{false, true} {
			var b []byte
			var err error
			if comparable {
				b, err = EncodeKey(sc, nil, types.NewDatum(ca.value))
			} else {
				b, err = EncodeValue(sc, nil, types.NewDatum(ca.value))
			}
			c.Assert(err, IsNil)
			chk := chunk.New([]*types.FieldType{ft}, 1, 1)
			decoder := NewDecoder(chk, time.UTC)
			_, err = decoder.DecodeOne(b, 0, ft)
			c.Assert(err, NotNil, Commentf("%v into type %d", ca.value, ca.tp))
			c.Assert(err.Error(), Matches, ".*does not match column type.*")
			c.Assert(chk.NumRows(), Equals, 0)
		}
	}

	// NULL fits any column.
	for _, tp := range []byte{mysql.TypeLonglong, mysql.TypeFloat, mysql.TypeVarchar} {
		ft := types.NewFieldType(tp)
		b, err := EncodeValue(sc, nil, types.NewDatum(nil))
		c.Assert(err, IsNil)
		decoder := NewDecoder(chunk.New([]*types.FieldType{ft}, 1, 1), time.UTC)
		_, err = decoder.DecodeOne(b, 0, ft)
		c.Assert(err, IsNil)
	}
}

func (s *testStoreBytesSuite) TestCutOneRejectsShortValues(c *C) {
	for _, b := range [][]byte{
		{intFlag, 1},
		{uintFlag, 1, 2, 3, 4, 5, 6, 7},
		{floatFlag},
		{varintFlag},
		{uvarintFlag},
		{varintFlag, 0x80},
		EncodeVarint([]byte{compactBytesFlag}, -5),
		EncodeVarint([]byte{compactBytesFlag}, math.MaxInt64),
		append(EncodeVarint([]byte{compactBytesFlag}, 4), "abc"...),
	} {
		_, _, err := CutOne(b)
		c.Assert(err, NotNil, Commentf("%v", b))
		_, _, err = DecodeOne(b)
		c.Assert(err, NotNil, Commentf("%v", b))
	}
	_, _, err := CutOne([]byte{intFlag, 1, 2, 3, 4, 5, 6, 7, 8})
	c.Assert(err, IsNil)
}
