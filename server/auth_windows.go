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

//go:build windows
// +build windows

package server

import "os"

// isPrivate always fails on Windows: it has no POSIX owner or mode bits, and
// the file's ACL isn't checked, so who else can read it is unknown. The
// generated root-password file and auto-TLS key are therefore not used there;
// set TINYSQL_ROOT_PASSWORD and ssl-cert/ssl-key instead.
func isPrivate(fi os.FileInfo, forbidden os.FileMode) bool {
	return false
}
