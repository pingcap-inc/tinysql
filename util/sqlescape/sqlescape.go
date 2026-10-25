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

// Package sqlescape quotes values that are put into SQL text built by the
// server itself, such as the internal statements run on restricted sessions.
package sqlescape

import "strings"

// Identifier returns name as a backtick-quoted identifier. A backtick inside
// the name is doubled, which the lexer reads back as one backtick.
func Identifier(name string) string {
	return "`" + strings.Replace(name, "`", "``", -1) + "`"
}

// stringReplacer escapes every character that can end or change a
// single-quoted string literal. Backslash escapes are honoured unless
// NO_BACKSLASH_ESCAPES is set, and a doubled quote works in both modes.
var stringReplacer = strings.NewReplacer(
	`\`, `\\`,
	`'`, `''`,
	"\x00", `\0`,
	"\n", `\n`,
	"\r", `\r`,
	"\x1a", `\Z`,
)

// String returns s as a single-quoted string literal.
func String(s string) string {
	return "'" + stringReplacer.Replace(s) + "'"
}
