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
	"net"
	"sync"
	"time"
)

const (
	// failuresBeforeDelay failed logins in a row from one address are free;
	// each later one waits before its error is sent, like MySQL's
	// connection_control plugin.
	failuresBeforeDelay = 5
	minFailureDelay     = time.Second
	// maxFailureDelay stays inside handshakeTimeout.
	maxFailureDelay = 8 * time.Second
	// failureMemory is how long a run of failures is remembered.
	failureMemory = 15 * time.Minute
	// maxHandshakesPerHost bounds a remote host's connections that haven't
	// logged in yet. Loopback is not bounded, so a local operator can't be
	// locked out.
	maxHandshakesPerHost = 8
	// maxTrackedHosts bounds the tracker's memory.
	maxTrackedHosts = 10000
)

// hostTracker keeps, per client address, the run of failed logins and the
// number of connections still in the handshake. A nil tracker tracks nothing.
type hostTracker struct {
	mu    sync.Mutex
	hosts map[string]*hostState
	now   func() time.Time
}

type hostState struct {
	failures    int
	lastFailure time.Time
	handshakes  int
}

func newHostTracker() *hostTracker {
	return &hostTracker{hosts: make(map[string]*hostState), now: time.Now}
}

// hostOf returns the IP of a client address, or the address itself.
func hostOf(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if tcp, ok := addr.(*net.TCPAddr); ok {
		return tcp.IP.String()
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// get returns host's state, creating it. The caller holds t.mu.
func (t *hostTracker) get(host string) *hostState {
	if st, ok := t.hosts[host]; ok {
		return st
	}
	if len(t.hosts) >= maxTrackedHosts {
		t.evictOne()
	}
	st := &hostState{}
	t.hosts[host] = st
	return st
}

// evictOne drops a host with no handshake in progress. Map iteration order is
// random, so this is a random such host, found in a few steps. The caller
// holds t.mu.
func (t *hostTracker) evictOne() {
	for host, st := range t.hosts {
		if st.handshakes == 0 {
			delete(t.hosts, host)
			return
		}
	}
}

// forget drops host's state if nothing in it matters any more. The caller
// holds t.mu.
func (t *hostTracker) forget(host string, st *hostState) {
	if st.handshakes == 0 && (st.failures == 0 || t.now().Sub(st.lastFailure) > failureMemory) {
		delete(t.hosts, host)
	}
}

// beginHandshake counts a new connection from host that hasn't logged in.
// It returns false, counting nothing, when a remote host already has
// maxHandshakesPerHost of them.
func (t *hostTracker) beginHandshake(host string) bool {
	if t == nil {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.get(host)
	if !isLoopback(host) && st.handshakes >= maxHandshakesPerHost {
		t.forget(host, st)
		return false
	}
	st.handshakes++
	return true
}

// endHandshake undoes beginHandshake once the connection logs in or closes.
func (t *hostTracker) endHandshake(host string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.hosts[host]; ok {
		if st.handshakes > 0 {
			st.handshakes--
		}
		t.forget(host, st)
	}
}

// loginFailed records a failed login from host and returns how long to wait
// before telling the client.
func (t *hostTracker) loginFailed(host string) time.Duration {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st := t.get(host)
	now := t.now()
	if now.Sub(st.lastFailure) > failureMemory {
		st.failures = 0
	}
	st.failures++
	st.lastFailure = now
	if st.failures <= failuresBeforeDelay {
		return 0
	}
	delay := minFailureDelay
	for i := failuresBeforeDelay + 1; i < st.failures && delay < maxFailureDelay; i++ {
		delay *= 2
	}
	if delay > maxFailureDelay {
		delay = maxFailureDelay
	}
	return delay
}

// loginSucceeded ends host's run of failures.
func (t *hostTracker) loginSucceeded(host string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.hosts[host]; ok {
		st.failures = 0
		t.forget(host, st)
	}
}
