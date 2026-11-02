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

package redact

import "testing"

func TestMask(t *testing.T) {
	cases := []struct{ in, out string }{
		{"select * from t where a = 'secret' and b = 42", "select * from t where a = ? and b = ?"},
		{`insert into t values ("x", 1.5e-3, 0xFF, 0b101, -7)`, `insert into t values (?, ?, ?, ?, -?)`},
		{"select `col1`, t2.c3 from `my``tbl` where c = 'it''s'", "select `col1`, t2.c3 from `my``tbl` where c = ?"},
		{`select 'a\'b', "c\"d"`, `select ?, ?`},
		{"select x'0A', b'01', _utf8'x'", "select x?, b?, _utf8?"},
		{"select 1 -- trailing 'comment'\n, 2 # more\n", "select ? -- ?\n, ? #?\n"},
		{"select 1 --\t'SECRET'", "select ? --\t?"},
		{"select 1 --", "select ? --"},
		{"select 1 --'SECRET'", "select ? --?"},
		{"select 1 /* plain 'SECRET' text */, 2 /**/", "select ? /*?*/, ? /**/"},
		{"select /*+ HINT(t1) */ c1 from t1", "select /*+ HINT(t1) */ c1 from t1"},
		{"select /*+ MEMORY_QUOTA(1024 MB) */ 'SECRET'", "select /*+ MEMORY_QUOTA(? MB) */ ?"},
		{"select 'unterminated", "select ?"},
		{"select `col` from t where a = 'x' and `unclosed 'SECRET", "select `col` from t where a = ? and `?"},
		{"select 1 /* unclosed 'SECRET' 42", "select ? /*?"},
		{"select /*!40001 SQL_NO_CACHE 'SECRET', 42 */ 1", "select /*!40001 SQL_NO_CACHE ?, ? */ ?"},
		{"select /*T![auto_rand] 'SECRET' */ 1", "select /*T![auto_rand] ? */ ?"},
		{"select /*!'SECRET'*/ 1", "select /*!?*/ ?"},
		{"", ""},
	}
	for _, c := range cases {
		if got := Mask(c.in); got != c.out {
			t.Errorf("Mask(%q) = %q, want %q", c.in, got, c.out)
		}
	}
}

func TestSQLSwitch(t *testing.T) {
	defer SetEnabled(true)
	if !Enabled() || SQL("select 1") != "select ?" {
		t.Fatal("redaction should be on by default")
	}
	SetEnabled(false)
	if SQL("select 1") != "select 1" {
		t.Fatal("redaction should be off")
	}
}
