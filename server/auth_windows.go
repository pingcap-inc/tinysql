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

// isPrivate always succeeds on Windows, which has no POSIX owner or mode bits;
// access there is governed by the ACLs of the user's profile directory.
func isPrivate(fi os.FileInfo, forbidden os.FileMode) bool {
	return true
}
