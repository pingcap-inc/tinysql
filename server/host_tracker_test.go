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
	"container/list"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/config"
)

type testHostTrackerSuite struct{}

var _ = Suite(&testHostTrackerSuite{})

func (s *testHostTrackerSuite) TestFailedLoginsAreDelayed(c *C) {
	now := time.Unix(1000, 0)
	t := newHostTracker()
	t.now = func() time.Time { return now }

	for _, host := range []string{"127.0.0.1", "10.0.0.1"} {
		var delays []time.Duration
		for i := 0; i < 10; i++ {
			delays = append(delays, t.loginFailed(host))
		}
		c.Assert(delays, DeepEquals, []time.Duration{0, 0, 0, 0, 0,
			time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 8 * time.Second}, Commentf(host))
		c.Assert(maxFailureDelay < handshakeTimeout, IsTrue)

		// A success ends the run.
		t.loginSucceeded(host)
		c.Assert(t.loginFailed(host), Equals, time.Duration(0))
	}

	// Other hosts are unaffected, and an old run is forgotten.
	for i := 0; i < 6; i++ {
		t.loginFailed("10.0.0.2")
	}
	c.Assert(t.loginFailed("10.0.0.3"), Equals, time.Duration(0))
	now = now.Add(failureMemory + time.Second)
	c.Assert(t.loginFailed("10.0.0.2"), Equals, time.Duration(0))
}

func (s *testHostTrackerSuite) TestHandshakesPerHost(c *C) {
	t := newHostTracker()
	for i := 0; i < maxHandshakesPerHost; i++ {
		c.Assert(t.beginHandshake("10.0.0.1"), IsTrue)
	}
	c.Assert(t.beginHandshake("10.0.0.1"), IsFalse)
	c.Assert(t.beginHandshake("10.0.0.2"), IsTrue)
	t.endHandshake("10.0.0.1")
	c.Assert(t.beginHandshake("10.0.0.1"), IsTrue)

	// Loopback is not capped.
	for i := 0; i < 100; i++ {
		c.Assert(t.beginHandshake("127.0.0.1"), IsTrue)
		c.Assert(t.beginHandshake("::1"), IsTrue)
	}

	// Finished hosts are dropped.
	for i := 0; i < maxHandshakesPerHost; i++ {
		t.endHandshake("10.0.0.1")
	}
	_, ok := t.hosts["10.0.0.1"]
	c.Assert(ok, IsFalse)
}

func (s *testHostTrackerSuite) TestTrackerIsBounded(c *C) {
	t := newHostTracker()
	for i := 0; i < maxTrackedHosts+500; i++ {
		t.loginFailed(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	c.Assert(len(t.hosts) <= maxTrackedHosts, IsTrue)

	// A nil tracker tracks nothing.
	var none *hostTracker
	c.Assert(none.beginHandshake("10.0.0.1"), IsTrue)
	c.Assert(none.loginFailed("10.0.0.1"), Equals, time.Duration(0))
	none.endHandshake("10.0.0.1")
	none.loginSucceeded("10.0.0.1")
}

// fakeAddrConn reports a chosen remote address.
type fakeAddrConn struct {
	net.Conn
	addr net.Addr
}

func (f fakeAddrConn) RemoteAddr() net.Addr { return f.addr }

// When every handshake slot is taken, the oldest idle handshake is closed for
// a new connection, so idle connections can't lock out a real login.
func (s *testHostTrackerSuite) TestPreAuthSlotsEvictTheOldest(c *C) {
	srv := &Server{cfg: &config.Config{MaxServerConnections: 4}, hosts: newHostTracker(), preAuth: list.New()}
	c.Assert(srv.preAuthLimit(), Equals, 3)

	newPipeConn := func(host string) (*clientConn, net.Conn) {
		server, client := net.Pipe()
		cc := srv.newConn(fakeAddrConn{server, &net.TCPAddr{IP: net.ParseIP(host), Port: 1}})
		return cc, client
	}
	var idle []*clientConn
	var peers []net.Conn
	for i := 0; i < 3; i++ {
		cc, peer := newPipeConn("127.0.0.1")
		c.Assert(srv.admitPreAuth(cc), IsTrue)
		idle = append(idle, cc)
		peers = append(peers, peer)
	}
	defer func() {
		for _, p := range peers {
			p.Close()
		}
	}()

	// The pool is full of young handshakes: refuse.
	cc, peer := newPipeConn("127.0.0.1")
	peers = append(peers, peer)
	c.Assert(srv.admitPreAuth(cc), IsFalse)

	// Once the oldest has waited preAuthEvictAge it is closed for the newcomer.
	idle[0].acceptedAt = time.Now().Add(-2 * preAuthEvictAge)
	c.Assert(srv.admitPreAuth(cc), IsTrue)
	c.Assert(srv.preAuth.Len(), Equals, 3)
	c.Assert(idle[0].preAuthElem, IsNil)
	_, err := peers[0].Read(make([]byte, 1))
	c.Assert(err, NotNil)

	// Leaving frees the slot, also for an evicted connection.
	srv.leavePreAuth(idle[0])
	srv.leavePreAuth(idle[1])
	c.Assert(srv.preAuth.Len(), Equals, 2)

	// The remote address is the one tracked.
	c.Assert(cc.remoteHost, Equals, "127.0.0.1")
	remote, peer := newPipeConn("192.0.2.7")
	peers = append(peers, peer)
	c.Assert(remote.remoteHost, Equals, "192.0.2.7")

	// No limit without max-server-connections.
	srv.cfg.MaxServerConnections = 0
	c.Assert(srv.preAuthLimit(), Equals, 0)
}

// An evicted connection's failure delay ends at once.
func (s *testHostTrackerSuite) TestEvictionEndsTheDelay(c *C) {
	srv := &Server{cfg: &config.Config{MaxServerConnections: 1}, hosts: newHostTracker(), preAuth: list.New()}
	server1, client1 := net.Pipe()
	defer client1.Close()
	old := srv.newConn(fakeAddrConn{server1, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1}})
	c.Assert(srv.admitPreAuth(old), IsTrue)
	old.acceptedAt = time.Now().Add(-2 * preAuthEvictAge)
	server2, client2 := net.Pipe()
	defer client2.Close()
	c.Assert(srv.admitPreAuth(srv.newConn(fakeAddrConn{server2, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2}})), IsTrue)
	select {
	case <-old.evicted:
	default:
		c.Fatal("the evicted connection's delay would not end")
	}
}

// The status server takes an attempt slot before it checks a password, so a
// host whose slots are all taken learns nothing about a guess, not even a
// right one, and a success gives its slot back before the route runs.
func (s *testHostTrackerSuite) TestStatusAuthGatesBeforeTheCompare(c *C) {
	cfg := &config.Config{}
	cfg.Security.RootPasswordHash = NativePasswordHash(testRootPassword)
	srv := &Server{cfg: cfg, hosts: newHostTracker()}
	served := 0
	handler := srv.statusAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		c.Assert(srv.hosts.hosts["192.0.2.7"].handshakes, Equals, maxHandshakesPerHost-1)
	}))
	get := func(user, password string) int {
		req := httptest.NewRequest("GET", "/status", nil)
		req.RemoteAddr = "192.0.2.7:4000"
		req.SetBasicAuth(user, password)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	for i := 0; i < maxHandshakesPerHost; i++ {
		c.Assert(srv.hosts.beginHandshake("192.0.2.7"), IsTrue)
	}
	c.Assert(get("root", testRootPassword), Equals, http.StatusTooManyRequests)
	c.Assert(get("root", "wrong"), Equals, http.StatusTooManyRequests)
	c.Assert(served, Equals, 0)
	c.Assert(srv.hosts.hosts["192.0.2.7"].failures, Equals, 0)

	// With a slot free the right password gets in and gives the slot back.
	srv.hosts.endHandshake("192.0.2.7")
	c.Assert(get("root", testRootPassword), Equals, http.StatusOK)
	c.Assert(served, Equals, 1)
	c.Assert(srv.hosts.hosts["192.0.2.7"].handshakes, Equals, maxHandshakesPerHost-1)

	// A wrong one is refused and counted.
	c.Assert(get("root", "wrong"), Equals, http.StatusUnauthorized)
	c.Assert(srv.hosts.hosts["192.0.2.7"].failures, Equals, 1)
	c.Assert(srv.hosts.hosts["192.0.2.7"].handshakes, Equals, maxHandshakesPerHost-1)
}
