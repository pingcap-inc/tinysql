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

//go:build tools
// +build tools

// Package tools records the build tools the Makefile builds from this module,
// so that go mod tidy keeps their go.sum entries. Without failpoint-ctl's, the
// default -mod=readonly can't build it and make failpoint-enable fails.
package tools

import (
	_ "github.com/pingcap/failpoint/failpoint-ctl"
)
