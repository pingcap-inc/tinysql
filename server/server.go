// The MIT License (MIT)
//
// Copyright (c) 2014 wandoulabs
// Copyright (c) 2014 siddontang
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

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

package server

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/domain"
	"github.com/pingcap/tidb/parser/mysql"
	"github.com/pingcap/tidb/parser/terror"

	"github.com/pingcap/tidb/util"
	"github.com/pingcap/tidb/util/logutil"
	"go.uber.org/zap"
)

var (
	baseConnID uint32
)

var (
	errInvalidSequence   = terror.ClassServer.New(mysql.ErrInvalidSequence, mysql.MySQLErrName[mysql.ErrInvalidSequence])
	errInvalidType       = terror.ClassServer.New(mysql.ErrInvalidType, mysql.MySQLErrName[mysql.ErrInvalidType])
	errAccessDenied      = terror.ClassServer.New(mysql.ErrAccessDenied, mysql.MySQLErrName[mysql.ErrAccessDenied])
	errConCount          = terror.ClassServer.New(mysql.ErrConCount, mysql.MySQLErrName[mysql.ErrConCount])
	errNetPacketTooLarge = terror.ClassServer.New(mysql.ErrNetPacketTooLarge, mysql.MySQLErrName[mysql.ErrNetPacketTooLarge])
)

const (
	// handshakeTimeout bounds the whole handshake, including TLS, so a client
	// cannot hold a connection open without logging in.
	handshakeTimeout = 10 * time.Second
	// preAuthMaxPacketSize caps packets read before the client has logged in. It
	// leaves room for libmysqlclient's 64 KiB of connection attributes.
	preAuthMaxPacketSize = 128 * 1024
)

// DefaultCapability is the capability of the server when it is created using the default configuration.
// When server is configured with SSL, the server will have extra capabilities compared to DefaultCapability.
const defaultCapability = mysql.ClientLongPassword | mysql.ClientLongFlag |
	mysql.ClientConnectWithDB | mysql.ClientProtocol41 |
	mysql.ClientTransactions | mysql.ClientSecureConnection | mysql.ClientFoundRows |
	mysql.ClientMultiStatements | mysql.ClientMultiResults | mysql.ClientLocalFiles |
	mysql.ClientConnectAtts | mysql.ClientPluginAuth | mysql.ClientInteractive

// Server is the MySQL protocol server
type Server struct {
	cfg        *config.Config
	tlsConfig  *tls.Config
	driver     IDriver
	listener   net.Listener
	socket     net.Listener
	rwlock     sync.RWMutex
	clients    map[uint32]*clientConn
	capability uint32
	dom        *domain.Domain

	// stopListenerCh is used when a critical error occurred, we don't want to exit the process, because there may be
	// a supervisor automatically restart it, then new client connection will be created, but we can't server it.
	// So we just stop the listener and store to force clients to chose other TiDB servers.
	stopListenerCh chan struct{}
	statusServer   *http.Server

	// connCount counts connections from accept until they close, including
	// ones still in the handshake.
	connCount int32
}

// ConnectionCount gets current connection count.
func (s *Server) ConnectionCount() int {
	s.rwlock.RLock()
	cnt := len(s.clients)
	s.rwlock.RUnlock()
	return cnt
}

// SetDomain use to set the server domain.
func (s *Server) SetDomain(dom *domain.Domain) {
	s.dom = dom
}

// newConn creates a new *clientConn from a net.Conn.
// It allocates a connection ID and random salt data for authentication.
func (s *Server) newConn(conn net.Conn) *clientConn {
	cc := newClientConn(s)
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if err := tcpConn.SetKeepAlive(true); err != nil {
			logutil.BgLogger().Error("failed to set tcp keep alive option", zap.Error(err))
		}
	}
	cc.setConn(conn)
	cc.pkt.setMaxPacketSize(preAuthMaxPacketSize)
	cc.salt = util.RandomBuf(20)
	return cc
}

// NewServer creates a new Server.
func NewServer(cfg *config.Config, driver IDriver) (*Server, error) {
	s := &Server{
		cfg:            cfg,
		driver:         driver,
		clients:        make(map[uint32]*clientConn),
		stopListenerCh: make(chan struct{}, 1),
	}

	if err := cfg.Valid(); err != nil {
		return nil, err
	}
	if len(cfg.Security.RootPasswordHash) == 0 {
		return nil, errors.New("the root password is not set")
	}
	var err error
	if s.tlsConfig, err = loadTLSConfig(&cfg.Security); err != nil {
		return nil, err
	}

	s.capability = defaultCapability
	if s.tlsConfig != nil {
		s.capability |= mysql.ClientSSL
	}

	if s.cfg.Host != "" && s.cfg.Port != 0 {
		addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
		if s.listener, err = net.Listen("tcp", addr); err == nil {
			logutil.BgLogger().Info("server is running MySQL protocol", zap.String("addr", addr))
		}
	} else {
		err = errors.New("Server not configured to listen on either -socket or -host and -port")
	}

	if err != nil {
		return nil, errors.Trace(err)
	}
	return s, nil
}

// loadTLSConfig returns the TLS config for MySQL clients, or nil when TLS is not
// configured.
func loadTLSConfig(sec *config.Security) (*tls.Config, error) {
	if sec.SSLCert == "" && sec.SSLKey == "" {
		if sec.SSLCA != "" {
			return nil, errors.New("ssl-ca is set but ssl-cert and ssl-key are not")
		}
		return nil, nil
	}
	if sec.SSLCert == "" || sec.SSLKey == "" {
		return nil, errors.New("ssl-cert and ssl-key must be set together")
	}
	cert, err := tls.LoadX509KeyPair(sec.SSLCert, sec.SSLKey)
	if err != nil {
		return nil, errors.Annotate(err, "load ssl-cert/ssl-key")
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if sec.SSLCA != "" {
		caPEM, err := os.ReadFile(sec.SSLCA)
		if err != nil {
			return nil, errors.Annotate(err, "read ssl-ca")
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.Errorf("no certificates found in ssl-ca %s", sec.SSLCA)
		}
		tlsConfig.ClientCAs = pool
		tlsConfig.ClientAuth = tls.VerifyClientCertIfGiven
	}
	logutil.BgLogger().Info("TLS is enabled for MySQL clients")
	return tlsConfig, nil
}

// Run runs the server.
func (s *Server) Run() error {

	// Start HTTP API to report tidb info such as TPS.
	if s.cfg.Status.ReportStatus {
		if err := s.startStatusHTTP(); err != nil {
			return err
		}
	}
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if opErr, ok := err.(*net.OpError); ok {
				if opErr.Err.Error() == "use of closed network connection" {
					return nil
				}
			}

			logutil.BgLogger().Error("accept failed", zap.Error(err))
			return errors.Trace(err)
		}
		if s.shouldStopListener() {
			err = conn.Close()
			terror.Log(errors.Trace(err))
			break
		}

		clientConn := s.newConn(conn)
		if !s.acquireConnSlot() {
			s.rejectTooManyConnections(clientConn)
			continue
		}

		go s.onConn(clientConn)
	}
	err := s.listener.Close()
	terror.Log(errors.Trace(err))
	s.listener = nil
	for {
		logutil.BgLogger().Error("listener stopped, waiting for manual kill.")
		time.Sleep(time.Minute)
	}
}

func (s *Server) shouldStopListener() bool {
	select {
	case <-s.stopListenerCh:
		return true
	default:
		return false
	}
}

// Close closes the server.
func (s *Server) Close() {
	s.rwlock.Lock()
	defer s.rwlock.Unlock()

	if s.listener != nil {
		err := s.listener.Close()
		terror.Log(errors.Trace(err))
		s.listener = nil
	}
	if s.socket != nil {
		err := s.socket.Close()
		terror.Log(errors.Trace(err))
		s.socket = nil
	}
	if s.statusServer != nil {
		err := s.statusServer.Close()
		terror.Log(errors.Trace(err))
		s.statusServer = nil
	}

}

// acquireConnSlot reserves a connection slot, or reports that the
// max-server-connections limit is reached.
func (s *Server) acquireConnSlot() bool {
	n := atomic.AddInt32(&s.connCount, 1)
	if limit := s.cfg.MaxServerConnections; limit > 0 && uint32(n) > limit {
		atomic.AddInt32(&s.connCount, -1)
		return false
	}
	return true
}

func (s *Server) rejectTooManyConnections(cc *clientConn) {
	logutil.BgLogger().Warn("too many connections, rejecting",
		zap.String("remoteAddr", cc.bufReadConn.RemoteAddr().String()), zap.Uint32("limit", s.cfg.MaxServerConnections))
	terror.Log(cc.bufReadConn.SetDeadline(time.Now().Add(time.Second)))
	terror.Log(cc.writeError(errConCount))
	terror.Log(cc.flush())
	terror.Log(cc.bufReadConn.Close())
}

// onConn runs in its own goroutine, handles queries from this connection.
func (s *Server) onConn(conn *clientConn) {
	ctx := logutil.WithConnID(context.Background(), conn.connectionID)
	defer atomic.AddInt32(&s.connCount, -1)
	defer func() {
		// clientConn.Run recovers its own panics; this covers the handshake.
		if r := recover(); r != nil {
			logutil.Logger(ctx).Error("connection handshake panic", zap.Any("err", r), zap.Stack("stack"))
			terror.Log(conn.bufReadConn.Close())
		}
	}()

	terror.Log(conn.bufReadConn.SetDeadline(time.Now().Add(handshakeTimeout)))
	if err := conn.handshake(ctx); err != nil {
		logutil.Logger(ctx).Info("handshake failed",
			zap.String("remoteAddr", conn.bufReadConn.RemoteAddr().String()), zap.String("user", conn.user), zap.Error(err))
		err = conn.Close()
		terror.Log(errors.Trace(err))
		return
	}
	terror.Log(conn.bufReadConn.SetDeadline(time.Time{}))
	conn.pkt.setMaxPacketSize(conn.maxAllowedPacket(ctx))

	logutil.Logger(ctx).Info("new connection", zap.String("remoteAddr", conn.bufReadConn.RemoteAddr().String()), zap.String("user", conn.user))

	defer func() {
		logutil.Logger(ctx).Info("connection closed")
	}()
	s.rwlock.Lock()
	s.clients[conn.connectionID] = conn
	s.rwlock.Unlock()
	conn.Run(ctx)
}

func killConn(conn *clientConn) {
	sessVars := conn.ctx.GetSessionVars()
	atomic.CompareAndSwapUint32(&sessVars.Killed, 0, 1)
}

// KillAllConnections kills all connections when server is not gracefully shutdown.
func (s *Server) KillAllConnections() {
	logutil.BgLogger().Info("[server] kill all connections.")

	// closeWithoutLock deletes from s.clients, so this needs the write lock.
	s.rwlock.Lock()
	defer s.rwlock.Unlock()
	for _, conn := range s.clients {
		atomic.StoreInt32(&conn.status, connStatusShutdown)
		if err := conn.closeWithoutLock(); err != nil {
			terror.Log(err)
		}
		killConn(conn)
	}
}

var gracefulCloseConnectionsTimeout = 15 * time.Second

// TryGracefulDown will try to gracefully close all connection first with timeout. if timeout, will close all connection directly.
func (s *Server) TryGracefulDown() {
	ctx, cancel := context.WithTimeout(context.Background(), gracefulCloseConnectionsTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.GracefulDown(ctx, done)
	}()
	select {
	case <-ctx.Done():
		s.KillAllConnections()
	case <-done:
		return
	}
}

// GracefulDown waits all clients to close.
func (s *Server) GracefulDown(ctx context.Context, done chan struct{}) {
	logutil.Logger(ctx).Info("[server] graceful shutdown.")

	count := s.ConnectionCount()
	for i := 0; count > 0; i++ {
		s.kickIdleConnection()

		count = s.ConnectionCount()
		if count == 0 {
			break
		}
		// Print information for every 30s.
		if i%30 == 0 {
			logutil.Logger(ctx).Info("graceful shutdown...", zap.Int("conn count", count))
		}
		ticker := time.After(time.Second)
		select {
		case <-ctx.Done():
			return
		case <-ticker:
		}
	}
	if done != nil {
		close(done)
	}
}

func (s *Server) kickIdleConnection() {
	var conns []*clientConn
	s.rwlock.RLock()
	for _, cc := range s.clients {
		if cc.ShutdownOrNotify() {
			// Shutdowned conn will be closed by us, and notified conn will exist themselves.
			conns = append(conns, cc)
		}
	}
	s.rwlock.RUnlock()

	for _, cc := range conns {
		err := cc.Close()
		if err != nil {
			logutil.BgLogger().Error("close connection", zap.Error(err))
		}
	}
}

func init() {
	serverMySQLErrCodes := map[terror.ErrCode]uint16{
		mysql.ErrNotAllowedCommand: mysql.ErrNotAllowedCommand,
		mysql.ErrAccessDenied:      mysql.ErrAccessDenied,
		mysql.ErrUnknownFieldType:  mysql.ErrUnknownFieldType,
		mysql.ErrInvalidSequence:   mysql.ErrInvalidSequence,
		mysql.ErrInvalidType:       mysql.ErrInvalidType,
		mysql.ErrConCount:          mysql.ErrConCount,
		mysql.ErrNetPacketTooLarge: mysql.ErrNetPacketTooLarge,
	}
	terror.ErrClassToMySQLCodes[terror.ClassServer] = serverMySQLErrCodes
}
