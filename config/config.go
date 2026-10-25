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
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/pingcap/errors"
	zaplog "github.com/pingcap/log"
	"github.com/pingcap/tidb/util/logutil"
	"go.uber.org/atomic"
)

// Config number limitations
const (
	// DefTxnTotalSizeLimit is the default value of TxnTxnTotalSizeLimit.
	DefTxnTotalSizeLimit = 1024 * 1024 * 1024
)

// Valid config maps
var (
	ValidStorage = map[string]bool{
		"mocktikv": true,
		"tikv":     true,
	}

	CheckTableBeforeDrop = false
	// checkBeforeDropLDFlag is a go build flag.
	checkBeforeDropLDFlag = "None"
)

// Config contains configuration options.
type Config struct {
	Host             string `toml:"host" json:"host"`
	AdvertiseAddress string `toml:"advertise-address" json:"advertise-address"`
	Port             uint   `toml:"port" json:"port"`
	Cors             string `toml:"cors" json:"cors"`
	Store            string `toml:"store" json:"store"`
	Path             string `toml:"path" json:"path"`
	Lease            string `toml:"lease" json:"lease"`
	// MaxServerConnections limits concurrent client connections, counted from
	// accept; 0 means unlimited.
	MaxServerConnections uint32   `toml:"max-server-connections" json:"max-server-connections"`
	Log                  Log      `toml:"log" json:"log"`
	Status               Status   `toml:"status" json:"status"`
	Security             Security `toml:"security" json:"security"`
}

// Log is the log section of config.
type Log struct {
	// Log level.
	Level string `toml:"level" json:"level"`
	// File log config.
	File logutil.FileLogConfig `toml:"file" json:"file"`
	// RedactSQL replaces the literals in SQL text written to the log with "?".
	RedactSQL bool `toml:"redact-sql" json:"redact-sql"`
	// GeneralLog logs every statement clients send, with connection and user.
	GeneralLog bool `toml:"general-log" json:"general-log"`
}

// The ErrConfigValidationFailed error is used so that external callers can do a type assertion
// to defer handling of this specific error when someone does not want strict type checking.
// This is needed only because logging hasn't been set up at the time we parse the config file.
// This should all be ripped out once strict config checking is made the default behavior.
type ErrConfigValidationFailed struct {
	confFile       string
	UndecodedItems []string
}

func (e *ErrConfigValidationFailed) Error() string {
	return fmt.Sprintf("config file %s contained unknown configuration options: %s", e.confFile, strings.Join(e.UndecodedItems, ", "))
}

// Status is the status section of the config.
type Status struct {
	StatusHost string `toml:"status-host" json:"status-host"`

	StatusPort uint `toml:"status-port" json:"status-port"`

	ReportStatus bool `toml:"report-status" json:"report-status"`

	// EnablePProf registers the Go pprof handlers on the status server. They
	// expose heap dumps and process arguments, so they are off by default.
	EnablePProf bool `toml:"enable-pprof" json:"enable-pprof"`
}

// Security is the security section of the config.
type Security struct {
	// SSLCA, SSLCert and SSLKey enable TLS for MySQL clients. Leave SSLCert and
	// SSLKey empty to disable TLS. SSLCA optionally verifies client certificates.
	SSLCA   string `toml:"ssl-ca" json:"ssl-ca"`
	SSLCert string `toml:"ssl-cert" json:"ssl-cert"`
	SSLKey  string `toml:"ssl-key" json:"ssl-key"`

	// RootPasswordHash is SHA1(SHA1(password)) of the root account, as used by
	// mysql_native_password. It is set at startup (see tidb-server) and is never
	// read from, or written to, a config file.
	RootPasswordHash []byte `toml:"-" json:"-"`
}

var defaultConf = Config{
	// Localhost only. Set host / status-host to "0.0.0.0" to accept connections
	// from other machines.
	Host:             "127.0.0.1",
	AdvertiseAddress: "",
	Port:             4000,
	Cors:             "",
	Store:            "mocktikv",
	Path:             "tinysql-data",
	Lease:            "45s",
	// The same as max_connections and MySQL's default.
	MaxServerConnections: 151,
	Log: Log{
		Level:      "info",
		File:       defaultFileLogConfig(),
		RedactSQL:  true,
		GeneralLog: false,
	},
	Status: Status{
		ReportStatus: false,
		StatusHost:   "127.0.0.1",
		StatusPort:   10080,
		EnablePProf:  false,
	},
}

var (
	globalConf = atomic.Value{}
)

// defaultFileLogConfig keeps a week of rotated log files, at most 10 of them.
func defaultFileLogConfig() logutil.FileLogConfig {
	c := logutil.NewFileLogConfig(logutil.DefaultLogMaxSize)
	c.MaxDays = 7
	c.MaxBackups = 10
	return c
}

// NewConfig creates a new config instance with default value.
func NewConfig() *Config {
	conf := defaultConf
	return &conf
}

// GetGlobalConfig returns the global configuration for this server.
// It should store configuration from command line and configuration file.
// Other parts of the system can read the global configuration use this function.
func GetGlobalConfig() *Config {
	return globalConf.Load().(*Config)
}

// StoreGlobalConfig stores a new config to the globalConf. It mostly uses in the test to avoid some data races.
func StoreGlobalConfig(config *Config) {
	globalConf.Store(config)
}

// Load loads config options from a toml file.
func (c *Config) Load(confFile string) error {
	metaData, err := toml.DecodeFile(confFile, c)
	// If any items in confFile file are not mapped into the Config struct, issue
	// an error and stop the server from starting.
	undecoded := metaData.Undecoded()
	if len(undecoded) > 0 && err == nil {
		var undecodedItems []string
		for _, item := range undecoded {
			undecodedItems = append(undecodedItems, item.String())
		}
		err = &ErrConfigValidationFailed{confFile, undecodedItems}
	}

	return err
}

// Valid checks the settings that would otherwise fail open: an empty host
// listens on every interface, and a wildcard CORS origin makes the status
// server's responses readable by any web page.
func (c *Config) Valid() error {
	if c.Host == "" || c.Port == 0 {
		return errors.New("host and port must be set")
	}
	if c.Status.ReportStatus && (c.Status.StatusHost == "" || c.Status.StatusPort == 0) {
		return errors.New("status-host and status-port must be set when report-status is true (set report-status = false to disable the status server)")
	}
	if strings.TrimSpace(c.Cors) == "*" {
		return errors.New(`cors = "*" is not allowed; set a single origin such as "http://127.0.0.1:3000"`)
	}
	return nil
}

// ToLogConfig converts *Log to *logutil.LogConfig.
func (l *Log) ToLogConfig() *logutil.LogConfig {
	return logutil.NewLogConfig(l.Level, "test", l.File, false, func(config *zaplog.Config) { config.DisableErrorVerbose = false })
}

func init() {
	globalConf.Store(&defaultConf)
	if checkBeforeDropLDFlag == "1" {
		CheckTableBeforeDrop = true
	}
}
