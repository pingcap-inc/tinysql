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

//go:build !race
// +build !race

package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
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

// testSecureTransportSuite runs servers that require TLS, one plain and one
// that also requires a client certificate.
type testSecureTransportSuite struct {
	store   kv.Storage
	domain  *domain.Domain
	servers []*Server
	dir     string
	// caPool trusts the CA that signs the server and client certificates.
	caPool     *x509.CertPool
	clientCert tls.Certificate
}

var _ = Suite(&testSecureTransportSuite{})

const (
	requireTLSAddr  = "127.0.0.1:4011"
	requireCertAddr = "127.0.0.1:4012"
)

func (s *testSecureTransportSuite) SetUpSuite(c *C) {
	var err error
	s.store, err = mockstore.NewMockTikvStore()
	c.Assert(err, IsNil)
	session.DisableStats4Test()
	s.domain, err = session.BootstrapSession(s.store)
	c.Assert(err, IsNil)
	s.dir = c.MkDir()

	caFile, serverCert, serverKey := s.writeCA(c)

	for _, sc := range []struct {
		port     uint
		withCert bool
	}{{4011, false}, {4012, true}} {
		cfg := config.NewConfig()
		cfg.Port = sc.port
		cfg.Security.RootPasswordHash = NativePasswordHash(testRootPassword)
		cfg.Security.SSLCert, cfg.Security.SSLKey = serverCert, serverKey
		cfg.Security.RequireSecureTransport = !sc.withCert
		if sc.withCert {
			cfg.Security.SSLCA = caFile
			cfg.Security.RequireClientCert = true
		}
		srv, err := NewServer(cfg, NewTiDBDriver(s.store))
		c.Assert(err, IsNil)
		s.servers = append(s.servers, srv)
		go srv.Run()
	}
	c.Assert(mysql.RegisterTLSConfig("tinysql-ca", &tls.Config{RootCAs: s.caPool, ServerName: "localhost"}), IsNil)
	c.Assert(mysql.RegisterTLSConfig("tinysql-client-cert", &tls.Config{
		RootCAs: s.caPool, ServerName: "localhost", Certificates: []tls.Certificate{s.clientCert}}), IsNil)
	for i := 0; i < 200; i++ {
		if s.ping(requireTLSAddr, "tinysql-ca") == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *testSecureTransportSuite) TearDownSuite(c *C) {
	for _, srv := range s.servers {
		srv.Close()
	}
	if s.domain != nil {
		s.domain.Close()
	}
	if s.store != nil {
		s.store.Close()
	}
}

// writeCA writes a CA and a server certificate it signs, and keeps a client
// certificate it signs. It returns the CA, server cert and server key files.
func (s *testSecureTransportSuite) writeCA(c *C) (caFile, certFile, keyFile string) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.Assert(err, IsNil)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "tinysql test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	c.Assert(err, IsNil)
	caCert, err := x509.ParseCertificate(caDER)
	c.Assert(err, IsNil)
	s.caPool = x509.NewCertPool()
	s.caPool.AddCert(caCert)

	issue := func(serial int64, usage x509.ExtKeyUsage) ([]byte, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		c.Assert(err, IsNil)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: "tinysql test"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
			DNSNames:     []string{"localhost"},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		c.Assert(err, IsNil)
		return der, key
	}
	writePEM := func(name, typ string, der []byte) string {
		file := filepath.Join(s.dir, name)
		c.Assert(os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0600), IsNil)
		return file
	}
	keyDER := func(key *ecdsa.PrivateKey) []byte {
		der, err := x509.MarshalECPrivateKey(key)
		c.Assert(err, IsNil)
		return der
	}

	caFile = writePEM("ca.pem", "CERTIFICATE", caDER)
	serverDER, serverKey := issue(2, x509.ExtKeyUsageServerAuth)
	certFile = writePEM("server-cert.pem", "CERTIFICATE", serverDER)
	keyFile = writePEM("server-key.pem", "EC PRIVATE KEY", keyDER(serverKey))
	clientDER, clientKey := issue(3, x509.ExtKeyUsageClientAuth)
	s.clientCert = tls.Certificate{Certificate: [][]byte{clientDER}, PrivateKey: clientKey}
	return caFile, certFile, keyFile
}

// ping logs in as root at addr, with the registered TLS config tlsName, or
// without TLS when tlsName is empty.
func (s *testSecureTransportSuite) ping(addr, tlsName string) error {
	dsn := getDSN(func(cfg *mysql.Config) {
		cfg.Addr = addr
		cfg.DBName = ""
		cfg.TLSConfig = tlsName
	})
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Ping()
}

func assertSecureTransportError(c *C, err error) {
	c.Assert(err, NotNil)
	mysqlErr, ok := err.(*mysql.MySQLError)
	c.Assert(ok, IsTrue, Commentf("%v", err))
	c.Assert(mysqlErr.Number, Equals, uint16(tmysql.ErrSecureTransportRequired))
}

func (s *testSecureTransportSuite) TestPlaintextIsRefusedWhenTLSIsRequired(c *C) {
	assertSecureTransportError(c, s.ping(requireTLSAddr, ""))
	c.Assert(s.ping(requireTLSAddr, "tinysql-ca"), IsNil)
}

func (s *testSecureTransportSuite) TestClientCertIsRequired(c *C) {
	// require-client-cert implies require-secure-transport.
	assertSecureTransportError(c, s.ping(requireCertAddr, ""))
	// TLS without a client certificate fails the TLS handshake.
	c.Assert(s.ping(requireCertAddr, "tinysql-ca"), NotNil)
	c.Assert(s.ping(requireCertAddr, "tinysql-client-cert"), IsNil)
}

func (s *testSecureTransportSuite) TestTLSSettingsAreValidated(c *C) {
	newCfg := func() *config.Config {
		cfg := config.NewConfig()
		cfg.Port = 4013
		cfg.Security.RootPasswordHash = NativePasswordHash(testRootPassword)
		return cfg
	}
	drv := NewTiDBDriver(s.store)

	cfg := newCfg()
	cfg.Security.RequireSecureTransport = true
	_, err := NewServer(cfg, drv)
	c.Assert(err, ErrorMatches, ".*need TLS.*")

	cfg = newCfg()
	cfg.Security.SSLCert, cfg.Security.SSLKey = filepath.Join(s.dir, "server-cert.pem"), filepath.Join(s.dir, "server-key.pem")
	cfg.Security.RequireClientCert = true
	_, err = NewServer(cfg, drv)
	c.Assert(err, ErrorMatches, ".*needs ssl-ca.*")
}

func (s *testSecureTransportSuite) TestLocalFilesIsNotAdvertised(c *C) {
	for _, srv := range s.servers {
		c.Assert(srv.capability&tmysql.ClientLocalFiles, Equals, uint32(0))
	}
}

func (s *testSecureTransportSuite) TestAutoTLSCert(c *C) {
	dir := c.MkDir()
	if runtime.GOOS == "windows" {
		// The key file's readers can't be checked there, so it is refused.
		_, _, _, err := LoadOrCreateTLSCert(dir)
		c.Assert(err, NotNil)
		return
	}
	certFile, keyFile, fp, err := LoadOrCreateTLSCert(dir)
	c.Assert(err, IsNil)
	c.Assert(fp, HasLen, 64)

	// The pair loads, covers localhost and 127.0.0.1, and lasts years.
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	c.Assert(err, IsNil)
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	c.Assert(err, IsNil)
	c.Assert(cert.VerifyHostname("localhost"), IsNil)
	c.Assert(cert.VerifyHostname("127.0.0.1"), IsNil)
	c.Assert(time.Until(cert.NotAfter) > 9*365*24*time.Hour, IsTrue)

	// The second start reuses it.
	_, _, fp2, err := LoadOrCreateTLSCert(dir)
	c.Assert(err, IsNil)
	c.Assert(fp2, Equals, fp)

	fi, err := os.Stat(keyFile)
	c.Assert(err, IsNil)
	c.Assert(fi.Mode().Perm(), Equals, os.FileMode(0600))

	// A key others can read is refused.
	c.Assert(os.Chmod(keyFile, 0644), IsNil)
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, ErrorMatches, ".*chmod 600.*")
	c.Assert(os.Chmod(keyFile, 0600), IsNil)

	// Half a pair is refused rather than silently replaced.
	c.Assert(os.Remove(certFile), IsNil)
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, ErrorMatches, ".*server-key.pem exists without its pair.*")
	c.Assert(os.Remove(keyFile), IsNil)

	// A certificate near expiry is replaced.
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, IsNil)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.Assert(err, IsNil)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(9), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	c.Assert(err, IsNil)
	c.Assert(os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644), IsNil)
	_, _, fp3, err := LoadOrCreateTLSCert(dir)
	c.Assert(err, IsNil)
	c.Assert(fp3, Not(Equals), certFingerprint(der))
	_, err = tls.LoadX509KeyPair(certFile, keyFile)
	c.Assert(err, IsNil)

	// A symlink in place of the key is refused.
	c.Assert(os.Remove(keyFile), IsNil)
	target := filepath.Join(c.MkDir(), "elsewhere-key.pem")
	c.Assert(os.WriteFile(target, []byte("planted"), 0600), IsNil)
	c.Assert(os.Symlink(target, keyFile), IsNil)
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, ErrorMatches, ".*not a regular file.*")

	// A data directory or tls/ directory others can write to is refused.
	tlsDir := filepath.Dir(keyFile)
	c.Assert(os.Chmod(tlsDir, 0777), IsNil)
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, ErrorMatches, ".*not writable by group or others.*")
	c.Assert(os.Chmod(tlsDir, 0700), IsNil)
	c.Assert(os.Chmod(dir, 0777), IsNil)
	_, _, _, err = LoadOrCreateTLSCert(dir)
	c.Assert(err, ErrorMatches, ".*not writable by group or others.*")
	c.Assert(os.Chmod(dir, 0700), IsNil)
}
