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

package server

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"math/big"
	"os"
	"path/filepath"

	"github.com/pingcap/errors"
)

const (
	// rootUser is the only account TinySQL accepts.
	rootUser = "root"
	// nativePasswordPlugin is the only authentication plugin TinySQL supports.
	nativePasswordPlugin = "mysql_native_password"
	// RootPasswordFileName is the file, inside the data directory, that holds a
	// generated root password.
	RootPasswordFileName = "root-password"

	generatedPasswordLen = 20
	passwordAlphabet     = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

// NativePasswordHash returns SHA1(SHA1(password)), the form in which
// mysql_native_password stores a password.
func NativePasswordHash(password string) []byte {
	stage1 := sha1.Sum([]byte(password))
	stage2 := sha1.Sum(stage1[:])
	return stage2[:]
}

// checkNativePassword verifies a mysql_native_password response. The client
// sends auth = SHA1(pw) XOR SHA1(salt + SHA1(SHA1(pw))); stored is SHA1(SHA1(pw)).
func checkNativePassword(salt, auth, stored []byte) bool {
	if len(auth) != sha1.Size || len(stored) != sha1.Size {
		return false
	}
	h := sha1.New()
	h.Write(salt)
	h.Write(stored)
	candidate := h.Sum(nil)
	for i := range candidate {
		candidate[i] ^= auth[i]
	}
	sum := sha1.Sum(candidate)
	return subtle.ConstantTimeCompare(sum[:], stored) == 1
}

// LoadOrCreateRootPassword returns the root password kept in
// <dir>/root-password, generating the file on first use. An existing file is
// only trusted if it is a regular file owned by this user and unreadable by
// anyone else.
func LoadOrCreateRootPassword(dir string) (password string, file string, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", errors.Trace(err)
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", "", errors.Trace(err)
	}
	if err = checkPrivateDir(dir); err != nil {
		return "", "", err
	}
	file = filepath.Join(dir, RootPasswordFileName)

	fi, err := os.Lstat(file)
	switch {
	case err == nil:
		if err = checkPrivateFile(file, fi); err != nil {
			return "", "", err
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return "", "", errors.Trace(err)
		}
		password = string(bytes.TrimRight(data, "\r\n"))
		if password == "" {
			return "", "", errors.Errorf("%s is empty", file)
		}
		return password, file, nil
	case os.IsNotExist(err):
		password, err = generatePassword()
		if err != nil {
			return "", "", err
		}
		// O_EXCL refuses to follow or overwrite anything already at this path,
		// including a symlink planted between the Lstat above and this call.
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return "", "", errors.Trace(err)
		}
		_, err = f.WriteString(password + "\n")
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return "", "", errors.Trace(err)
		}
		return password, file, nil
	default:
		return "", "", errors.Trace(err)
	}
}

func checkPrivateFile(file string, fi os.FileInfo) error {
	if !fi.Mode().IsRegular() {
		return errors.Errorf("%s is not a regular file", file)
	}
	if !isPrivate(fi, 0077) {
		return errors.Errorf("%s must be owned by the current user and not accessible by group or others (chmod 600)", file)
	}
	return nil
}

func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return errors.Trace(err)
	}
	if !fi.IsDir() {
		return errors.Errorf("%s is not a directory", dir)
	}
	if !isPrivate(fi, 0022) {
		return errors.Errorf("%s must be owned by the current user and not writable by group or others", dir)
	}
	return nil
}

func generatePassword() (string, error) {
	buf := make([]byte, generatedPasswordLen)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range buf {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", errors.Trace(err)
		}
		buf[i] = passwordAlphabet[n.Int64()]
	}
	return string(buf), nil
}
