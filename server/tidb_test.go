// Copyright 2015 PingCAP, Inc.
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
// +build !race

package server

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/go-sql-driver/mysql"
	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/kv"
	tmysql "github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/session"
	"github.com/pingcap/tidb/store/mockstore"
)

type TidbTestSuite struct {
	tidbdrv *TiDBDriver
	server  *Server
	domain  *domain.Domain
	store   kv.Storage
}

var suite = new(TidbTestSuite)
var _ = Suite(suite)

func (ts *TidbTestSuite) SetUpSuite(c *C) {
	var err error
	ts.store, err = mockstore.NewMockTikvStore()
	session.DisableStats4Test()
	c.Assert(err, IsNil)
	ts.domain, err = session.BootstrapSession(ts.store)
	c.Assert(err, IsNil)
	ts.tidbdrv = NewTiDBDriver(ts.store)
	cfg := config.NewConfig()
	cfg.Port = 4001
	cfg.Status.ReportStatus = true
	cfg.Status.StatusPort = 10090
	cfg.Security.RootPasswordHash = NativePasswordHash(testRootPassword)

	server, err := NewServer(cfg, ts.tidbdrv)
	c.Assert(err, IsNil)
	ts.server = server
	go ts.server.Run()
	waitUntilServerOnline(cfg.Status.StatusPort)
}

func (ts *TidbTestSuite) TearDownSuite(c *C) {
	if ts.store != nil {
		ts.store.Close()
	}
	if ts.domain != nil {
		ts.domain.Close()
	}
	if ts.server != nil {
		ts.server.Close()
	}
}

func (ts *TidbTestSuite) TestIssues(c *C) {
	c.Parallel()
	runTestIssue3662(c)
}

func (ts *TidbTestSuite) TestAuthentication(c *C) {
	c.Parallel()
	for _, overrider := range []configOverrider{
		func(config *mysql.Config) { config.Passwd = "wrong" },
		func(config *mysql.Config) { config.Passwd = "" },
		func(config *mysql.Config) { config.User = "other" },
	} {
		db, err := sql.Open("mysql", getDSN(overrider))
		c.Assert(err, IsNil)
		err = db.Ping()
		db.Close()
		c.Assert(err, NotNil)
		mysqlErr, ok := err.(*mysql.MySQLError)
		c.Assert(ok, IsTrue, Commentf("%v", err))
		c.Assert(mysqlErr.Number, Equals, uint16(tmysql.ErrAccessDenied))
	}

	db, err := sql.Open("mysql", getDSN())
	c.Assert(err, IsNil)
	defer db.Close()
	c.Assert(db.Ping(), IsNil)
}

func (ts *TidbTestSuite) TestStatusServerHasNoPProfByDefault(c *C) {
	c.Parallel()
	resp, err := http.Get("http://127.0.0.1:10090/debug/pprof/cmdline")
	c.Assert(err, IsNil)
	resp.Body.Close()
	c.Assert(resp.StatusCode, Equals, http.StatusNotFound)
}

// rawLogin opens a connection to the test server and logs in as root with a
// hand-written HandshakeResponse41, so the test can then send raw packets.
func rawLogin(c *C) net.Conn {
	conn, err := net.Dial("tcp", "127.0.0.1:4001")
	c.Assert(err, IsNil)
	c.Assert(conn.SetDeadline(time.Now().Add(10*time.Second)), IsNil)
	greeting := readRawPacket(c, conn)
	// version(1) server-version\0 conn-id(4) salt1(8) filler(1) cap(2) charset(1) status(2) cap(2) authlen(1) reserved(10) salt2(12)\0
	pos := 1 + bytes.IndexByte(greeting[1:], 0) + 1 + 4
	salt := append([]byte{}, greeting[pos:pos+8]...)
	pos += 8 + 1 + 2 + 1 + 2 + 2 + 1 + 10
	salt = append(salt, greeting[pos:pos+12]...)

	capability := tmysql.ClientProtocol41 | tmysql.ClientSecureConnection | tmysql.ClientLongPassword
	resp := []byte{byte(capability), byte(capability >> 8), byte(capability >> 16), byte(capability >> 24), 0, 0, 0, 1, 33}
	resp = append(resp, make([]byte, 23)...)
	resp = append(resp, []byte("root\x00")...)
	auth := scramble(salt, testRootPassword)
	resp = append(resp, byte(len(auth)))
	resp = append(resp, auth...)
	writeRawPacket(c, conn, 1, resp)
	ok := readRawPacket(c, conn)
	c.Assert(ok[0], Equals, byte(tmysql.OKHeader), Commentf("login failed: %q", ok))
	return conn
}

func readRawPacket(c *C, conn net.Conn) []byte {
	var header [4]byte
	_, err := io.ReadFull(conn, header[:])
	c.Assert(err, IsNil)
	data := make([]byte, int(header[0])|int(header[1])<<8|int(header[2])<<16)
	_, err = io.ReadFull(conn, data)
	c.Assert(err, IsNil)
	return data
}

func writeRawPacket(c *C, conn net.Conn, seq byte, payload []byte) {
	header := []byte{byte(len(payload)), byte(len(payload) >> 8), byte(len(payload) >> 16), seq}
	_, err := conn.Write(append(header, payload...))
	c.Assert(err, IsNil)
}

// An empty command packet gets an error and the connection keeps working.
func (ts *TidbTestSuite) TestEmptyCommandPacket(c *C) {
	c.Parallel()
	conn := rawLogin(c)
	defer conn.Close()
	writeRawPacket(c, conn, 0, nil)
	c.Assert(readRawPacket(c, conn)[0], Equals, byte(tmysql.ErrHeader))
	writeRawPacket(c, conn, 0, []byte{tmysql.ComPing})
	c.Assert(readRawPacket(c, conn)[0], Equals, byte(tmysql.OKHeader))
}

// Without CLIENT_MULTI_STATEMENTS (the Go driver's default) a statement batch
// is refused; with it, the batch runs.
func (ts *TidbTestSuite) TestMultiStatementsNeedTheCapability(c *C) {
	c.Parallel()
	db, err := sql.Open("mysql", getDSN())
	c.Assert(err, IsNil)
	defer db.Close()
	_, err = db.Exec("select 1; select 2")
	c.Assert(err, NotNil)

	multi, err := sql.Open("mysql", getDSN(func(config *mysql.Config) { config.MultiStatements = true }))
	c.Assert(err, IsNil)
	defer multi.Close()
	// Two reads, no DDL: concurrent DDL would hit the skeleton's unimplemented
	// lock resolution in the parallel tests.
	_, err = multi.Exec("select 1; select 2")
	c.Assert(err, IsNil)
}

// A database name from the handshake is quoted before USE runs it.
func (ts *TidbTestSuite) TestHandshakeDBNameIsQuoted(c *C) {
	c.Parallel()
	db, err := sql.Open("mysql", getDSN(func(config *mysql.Config) { config.DBName = "test`; create database injected_db; -- " }))
	c.Assert(err, IsNil)
	c.Assert(db.Ping(), NotNil)
	db.Close()

	db, err = sql.Open("mysql", getDSN())
	c.Assert(err, IsNil)
	defer db.Close()
	// count(*) would always be 0 on the course skeleton (aggregation is a
	// proj5 TODO), so look for the row itself.
	var name string
	c.Assert(db.QueryRow("select schema_name from information_schema.schemata where schema_name = 'injected_db'").Scan(&name), Equals, sql.ErrNoRows)
}

func (ts *TidbTestSuite) TestNewServerRequiresRootPassword(c *C) {
	cfg := config.NewConfig()
	cfg.Port = 4002
	_, err := NewServer(cfg, ts.tidbdrv)
	c.Assert(err, NotNil)
}

func (ts *TidbTestSuite) TestDBNameEscape(c *C) {
	c.Parallel()
	runTestDBNameEscape(c)
}

func (ts *TidbTestSuite) TestResultFieldTableIsNull(c *C) {
	c.Parallel()
	runTestResultFieldTableIsNull(c)
}

func (ts *TidbTestSuite) TestShowTablesFlen(c *C) {
	qctx, err := ts.tidbdrv.OpenCtx(uint64(0), 0, uint8(tmysql.DefaultCollationID), "test", nil)
	c.Assert(err, IsNil)
	_, err = qctx.Execute(context.Background(), "use test;")
	c.Assert(err, IsNil)

	ctx := context.Background()
	testSQL := "create table abcdefghijklmnopqrstuvwxyz (i int)"
	_, err = qctx.Execute(ctx, testSQL)
	c.Assert(err, IsNil)
	rs, err := qctx.Execute(ctx, "show tables")
	c.Assert(err, IsNil)
	req := rs[0].NewChunk()
	err = rs[0].Next(ctx, req)
	c.Assert(err, IsNil)
	cols := rs[0].Columns()
	c.Assert(err, IsNil)
	c.Assert(len(cols), Equals, 1)
	c.Assert(int(cols[0].ColumnLength), Equals, 26*tmysql.MaxBytesOfCharacter)
}

func (ts *TidbTestSuite) TestNullFlag(c *C) {
	// issue #9689
	qctx, err := ts.tidbdrv.OpenCtx(uint64(0), 0, uint8(tmysql.DefaultCollationID), "test", nil)
	c.Assert(err, IsNil)

	ctx := context.Background()
	rs, err := qctx.Execute(ctx, "select 1")
	c.Assert(err, IsNil)
	cols := rs[0].Columns()
	c.Assert(len(cols), Equals, 1)
	expectFlag := uint16(tmysql.NotNullFlag | tmysql.BinaryFlag)
	c.Assert(dumpFlag(cols[0].Type, cols[0].Flag), Equals, expectFlag)
}
