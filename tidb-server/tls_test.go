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

package main

import (
	"path/filepath"
	"runtime"

	. "github.com/pingcap/check"
	"github.com/pingcap/tidb/config"
	"github.com/pingcap/tidb/sessionctx/variable"
)

type testTLSSuite struct{}

var _ = Suite(&testTLSSuite{})

func (s *testTLSSuite) TestTLSSystemVariables(c *C) {
	defer func(old *config.Config) { cfg = old }(cfg)
	sysVar := func(name string) string { return variable.SysVars[name].Value }
	// setupTLS sets these globals; put them back for the rest of the package.
	names := []string{"have_ssl", "have_openssl", "ssl_ca", "ssl_cert", "ssl_key", variable.RequireSecureTransport}
	saved := make([]string, len(names))
	for i, name := range names {
		saved[i] = sysVar(name)
	}
	defer func() {
		for i, name := range names {
			variable.SysVars[name].Value = saved[i]
		}
	}()

	// No certificate and no auto-TLS: TLS is off and says so.
	cfg = config.NewConfig()
	cfg.Path = c.MkDir()
	cfg.Security.AutoTLS = false
	setupTLS()
	c.Assert(sysVar("have_ssl"), Equals, "DISABLED")
	c.Assert(sysVar("have_openssl"), Equals, "DISABLED")
	c.Assert(sysVar("ssl_cert"), Equals, "")
	c.Assert(sysVar(variable.RequireSecureTransport), Equals, "OFF")
	c.Assert(sysVar("tls_version"), Equals, "TLSv1.2,TLSv1.3")
	c.Assert(sysVar(variable.LocalInFile), Equals, "0")

	// An in-memory store gets no auto-generated certificate.
	cfg = config.NewConfig()
	cfg.Path = ""
	setupTLS()
	c.Assert(sysVar("have_ssl"), Equals, "DISABLED")

	if runtime.GOOS == "windows" {
		return
	}
	// The default: an on-disk mocktikv store gets a self-signed certificate.
	cfg = config.NewConfig()
	cfg.Path = c.MkDir()
	cfg.Security.RequireSecureTransport = true
	setupTLS()
	c.Assert(cfg.Security.SSLCert, Equals, filepath.Join(cfg.Path, "tls", "server-cert.pem"))
	c.Assert(cfg.Security.SSLKey, Equals, filepath.Join(cfg.Path, "tls", "server-key.pem"))
	c.Assert(sysVar("have_ssl"), Equals, "YES")
	c.Assert(sysVar("have_openssl"), Equals, "YES")
	c.Assert(sysVar("ssl_cert"), Equals, cfg.Security.SSLCert)
	c.Assert(sysVar("ssl_key"), Equals, cfg.Security.SSLKey)
	c.Assert(sysVar(variable.RequireSecureTransport), Equals, "ON")
}
