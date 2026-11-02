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

package server

import (
	"bytes"
	"context"

	. "github.com/pingcap/check"
	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/kv"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/session"
)

func (s *PacketIOTestSuite) TestPacketSizeLimit(c *C) {
	// A header declaring 100 bytes is refused before the payload is read.
	var in bytes.Buffer
	in.Write([]byte{100, 0x00, 0x00, 0x00})
	pkt := newPacketIO(newBufferedReadConn(&bytesConn{in}))
	pkt.setMaxPacketSize(50)
	_, err := pkt.readPacket()
	c.Assert(err, NotNil)
	c.Assert(errNetPacketTooLarge.Equal(err), IsTrue)

	// The limit applies to all fragments of a packet together.
	in.Reset()
	buf := make([]byte, mysql.MaxPayloadLen+4)
	buf[0], buf[1], buf[2], buf[3] = 0xff, 0xff, 0xff, 0
	in.Write(buf)
	in.Write([]byte{0x01, 0x00, 0x00, 0x01, 0x0a})
	pkt = newPacketIO(newBufferedReadConn(&bytesConn{in}))
	pkt.setMaxPacketSize(mysql.MaxPayloadLen)
	_, err = pkt.readPacket()
	c.Assert(err, NotNil)
	c.Assert(errNetPacketTooLarge.Equal(err), IsTrue)
}

func (ts *ConnTestSuite) TestDispatchEmptyPacket(c *C) {
	c.Parallel()
	cc := &clientConn{}
	c.Assert(cc.dispatch(context.Background(), []byte{}), Equals, mysql.ErrMalformPacket)
}

func (ts *ConnTestSuite) TestLastStmtShortPacket(c *C) {
	c.Parallel()
	for _, cmd := range []byte{mysql.ComStmtClose, mysql.ComStmtReset} {
		s := getLastStmtInConn{&clientConn{lastPacket: []byte{cmd}}}.String()
		c.Assert(s, Equals, mysql.Command2Str[cmd])
	}
}

func (ts *ConnTestSuite) TestConnSlotLimit(c *C) {
	c.Parallel()
	s := &Server{cfg: &config.Config{MaxServerConnections: 2}}
	c.Assert(s.acquireConnSlot(), IsTrue)
	c.Assert(s.acquireConnSlot(), IsTrue)
	c.Assert(s.acquireConnSlot(), IsFalse)
	c.Assert(s.connCount, Equals, int32(2))

	s = &Server{cfg: &config.Config{MaxServerConnections: 0}}
	for i := 0; i < 1000; i++ {
		c.Assert(s.acquireConnSlot(), IsTrue)
	}
}

func (ts *ConnTestSuite) TestGracefulDownNilDone(c *C) {
	c.Parallel()
	s := &Server{clients: make(map[uint32]*clientConn)}
	s.GracefulDown(context.Background(), nil)
}

func (ts *ConnTestSuite) TestFieldListBeforeFirstStatement(c *C) {
	se, err := session.CreateSession4Test(ts.store)
	c.Assert(err, IsNil)
	defer se.Close()
	_, err = se.FieldList("no_such_table")
	c.Assert(err, NotNil)
}

// Client-supplied names are quoted and truncated in the log, and an unknown
// command's payload isn't logged.
func (ts *ConnTestSuite) TestLastStmtNamesAreQuoted(c *C) {
	c.Parallel()
	str := func(packet ...byte) string { return getLastStmtInConn{&clientConn{lastPacket: packet}}.String() }
	c.Assert(str(append([]byte{mysql.ComInitDB}, "db\nforged line"...)...), Equals, `Use "db\nforged line"`)
	c.Assert(str(append([]byte{mysql.ComFieldList}, "t\x00"...)...), Equals, `ListFields "t\x00"`)
	long := str(append([]byte{mysql.ComInitDB}, bytes.Repeat([]byte("x"), 200)...)...)
	c.Assert(long, Equals, "Use \""+string(bytes.Repeat([]byte("x"), maxLoggedNameLen))+"\"...")
	c.Assert(str(200, 's', 'e', 'c'), Equals, "cmd=200 len=3")
}

// "command dispatched failed" leaves a write conflict's key out of the log.
func (ts *ConnTestSuite) TestDispatchErrForLog(c *C) {
	c.Parallel()
	conflict := kv.ErrWriteConflict.FastGenByArgs(1, 2, 0, "{tableID=45, indexID=1, indexValues={SECRET, }}")
	c.Assert(dispatchErrForLog(conflict), Equals, "[kv:9007]Write conflict (key not logged)")
	c.Assert(dispatchErrForLog(errors.New("bad value 'SECRET'")), Not(Matches), ".*SECRET.*")
}
