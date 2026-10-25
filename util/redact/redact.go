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

// Package redact masks the data values in SQL text before it is logged.
package redact

import (
	"regexp"
	"strings"
	"sync/atomic"
)

// specialCommentPrefix matches the start of a MySQL-specific (/*!...*/) or
// TiDB-specific (/*T!...*/) comment body, whose content runs as SQL.
var specialCommentPrefix = regexp.MustCompile(`^(T!|!)(M?[0-9]{5,6})?`)

// disabled is 1 when SQL redaction is turned off (-redact-sql=false).
var disabled int32

// SetEnabled turns SQL redaction in logs on or off. It is on by default.
func SetEnabled(on bool) {
	var v int32
	if !on {
		v = 1
	}
	atomic.StoreInt32(&disabled, v)
}

// Enabled reports whether SQL redaction is on.
func Enabled() bool {
	return atomic.LoadInt32(&disabled) == 0
}

// SQL returns sql with its literals masked when redaction is on, and sql
// unchanged otherwise. Use it for every SQL text written to a log.
func SQL(sql string) string {
	if !Enabled() {
		return sql
	}
	return Mask(sql)
}

// Mask replaces every string, number, hexadecimal and bit literal in sql with
// "?". Identifiers, keywords, quoted identifiers and comments are kept, so the
// shape of the statement stays readable. It does not need valid SQL.
func Mask(sql string) string {
	var b strings.Builder
	b.Grow(len(sql))
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"':
			i, _ = skipQuoted(sql, i, c, true)
			b.WriteByte('?')
		case c == '`':
			j, closed := skipQuoted(sql, i, '`', false)
			if !closed {
				// Without its closing backtick the rest may be data, not a name.
				b.WriteString("`?")
				return b.String()
			}
			b.WriteString(sql[i:j])
			i = j
		case c == '#' || (c == '-' && strings.HasPrefix(sql[i:], "-- ")):
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				j = len(sql) - i
			}
			b.WriteString(sql[i : i+j])
			i += j
		case c == '/' && strings.HasPrefix(sql[i:], "/*"):
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				// An unclosed comment may swallow the rest of the input.
				b.WriteString("/*?")
				return b.String()
			}
			body := sql[i+2 : i+2+j]
			if prefix := specialCommentPrefix.FindString(body); prefix != "" {
				// /*! ... */ and /*T! ... */ bodies are executed as SQL.
				b.WriteString("/*" + prefix + Mask(body[len(prefix):]) + "*/")
			} else {
				b.WriteString(sql[i : i+2+j+2])
			}
			i += 2 + j + 2
		case isDigit(c) && (i == 0 || !isIdentChar(sql[i-1])):
			i = skipNumber(sql, i)
			b.WriteByte('?')
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// skipQuoted returns the index just past the quoted text starting at i, and
// whether the closing quote was found. A doubled quote stands for one quote;
// backslash escapes apply to strings.
func skipQuoted(s string, i int, q byte, backslash bool) (int, bool) {
	for i++; i < len(s); i++ {
		switch {
		case backslash && s[i] == '\\':
			i++
		case s[i] == q:
			if i+1 < len(s) && s[i+1] == q {
				i++
				continue
			}
			return i + 1, true
		}
	}
	return len(s), false
}

// skipNumber returns the index just past a number literal starting at i:
// decimal, exponent, 0x... or 0b... forms.
func skipNumber(s string, i int) int {
	if s[i] == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X' || s[i+1] == 'b' || s[i+1] == 'B') {
		i += 2
	}
	for i < len(s) {
		c := s[i]
		switch {
		case isIdentChar(c) || c == '.':
			i++
		case (c == '+' || c == '-') && (s[i-1] == 'e' || s[i-1] == 'E'):
			i++
		default:
			return i
		}
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isIdentChar(c byte) bool {
	return c == '_' || c == '$' || isDigit(c) || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}
