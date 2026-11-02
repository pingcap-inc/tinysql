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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/pingcap/errors"
)

const (
	autoTLSDirName  = "tls"
	autoTLSCertName = "server-cert.pem"
	autoTLSKeyName  = "server-key.pem"
	// autoTLSValidity is how long a generated certificate is valid; MySQL's
	// auto_generate_certs uses 10 years too.
	autoTLSValidity = 10 * 365 * 24 * time.Hour
	// autoTLSRenewBefore regenerates a certificate this close to expiry.
	autoTLSRenewBefore = 30 * 24 * time.Hour
)

// LoadOrCreateTLSCert returns a self-signed certificate and key for MySQL
// clients, kept in <dir>/tls and generated on first use or when the kept one
// is about to expire. The key is only trusted if it is a regular file owned by
// this user and unreadable by anyone else. fingerprint is the certificate's
// SHA-256, for clients that pin it.
func LoadOrCreateTLSCert(dir string) (certFile, keyFile, fingerprint string, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", "", errors.Trace(err)
	}
	if err = checkPrivateDir(dir); err != nil {
		return "", "", "", err
	}
	tlsDir := filepath.Join(dir, autoTLSDirName)
	if err = os.MkdirAll(tlsDir, 0700); err != nil {
		return "", "", "", errors.Trace(err)
	}
	if err = checkPrivateDir(tlsDir); err != nil {
		return "", "", "", err
	}
	certFile = filepath.Join(tlsDir, autoTLSCertName)
	keyFile = filepath.Join(tlsDir, autoTLSKeyName)

	certFI, certErr := os.Lstat(certFile)
	keyFI, keyErr := os.Lstat(keyFile)
	switch {
	case certErr == nil && keyErr == nil:
		if err = checkPrivateFile(keyFile, keyFI); err != nil {
			return "", "", "", err
		}
		if !certFI.Mode().IsRegular() {
			return "", "", "", errors.Errorf("%s is not a regular file", certFile)
		}
		der, err := readCertDER(certFile)
		if err != nil {
			return "", "", "", err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return "", "", "", errors.Annotatef(err, "parse %s", certFile)
		}
		if time.Until(cert.NotAfter) > autoTLSRenewBefore {
			return certFile, keyFile, certFingerprint(der), nil
		}
		// Expired or about to: replace both files.
		if err = os.Remove(certFile); err != nil {
			return "", "", "", errors.Trace(err)
		}
		if err = os.Remove(keyFile); err != nil {
			return "", "", "", errors.Trace(err)
		}
	case os.IsNotExist(certErr) && os.IsNotExist(keyErr):
	case certErr == nil || keyErr == nil:
		have := certFile
		if keyErr == nil {
			have = keyFile
		}
		return "", "", "", errors.Errorf("%s exists without its pair; remove it to generate a new certificate and key", have)
	case certErr != nil && !os.IsNotExist(certErr):
		return "", "", "", errors.Trace(certErr)
	default:
		return "", "", "", errors.Trace(keyErr)
	}

	der, err := writeSelfSignedCert(certFile, keyFile)
	if err != nil {
		return "", "", "", err
	}
	return certFile, keyFile, certFingerprint(der), nil
}

func readCertDER(file string) ([]byte, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, errors.Trace(err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.Errorf("%s holds no PEM certificate", file)
	}
	return block.Bytes, nil
}

// writeSelfSignedCert generates an ECDSA P-256 key and a self-signed
// certificate for localhost, and returns the certificate's DER bytes.
func writeSelfSignedCert(certFile, keyFile string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, errors.Trace(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, errors.Trace(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "TinySQL_Auto_Generated_Server_Certificate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(autoTLSValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, errors.Trace(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, errors.Trace(err)
	}
	// Write the key first, with O_EXCL so nothing already at the path (such as
	// a planted symlink) is followed or overwritten.
	if err = writeNewFile(keyFile, 0600, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return nil, err
	}
	if err = writeNewFile(certFile, 0644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return nil, err
	}
	return der, nil
}

func writeNewFile(file string, perm os.FileMode, data []byte) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return errors.Trace(err)
	}
	_, err = f.Write(data)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return errors.Trace(err)
}

func certFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}
