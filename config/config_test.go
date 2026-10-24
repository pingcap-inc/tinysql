// Copyright 2017 PingCAP, Inc.
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

package config

import (
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/pingcap/check"
)

var _ = Suite(&testConfigSuite{})

type testConfigSuite struct{}

func TestT(t *testing.T) {
	CustomVerboseFlag = true
	TestingT(t)
}

func (s *testConfigSuite) TestConfig(c *C) {
	conf := new(Config)
	_, localFile, _, _ := runtime.Caller(0)

	configFile := filepath.Join(filepath.Dir(localFile), "config.toml.example")
	c.Assert(conf.Load(configFile), IsNil)

	// Make sure the example config is the same as default config.
	c.Assert(conf, DeepEquals, GetGlobalConfig())
}

// TinySQL has no authentication, so it must not listen on other interfaces
// unless the user asks for it.
func (s *testConfigSuite) TestDefaultListenLocalhostOnly(c *C) {
	conf := NewConfig()
	c.Assert(conf.Host, Equals, "127.0.0.1")
	c.Assert(conf.Status.StatusHost, Equals, "127.0.0.1")

	example := new(Config)
	_, localFile, _, _ := runtime.Caller(0)
	c.Assert(example.Load(filepath.Join(filepath.Dir(localFile), "config.toml.example")), IsNil)
	c.Assert(example.Host, Equals, "127.0.0.1")
	c.Assert(example.Status.StatusHost, Equals, "127.0.0.1")
}
