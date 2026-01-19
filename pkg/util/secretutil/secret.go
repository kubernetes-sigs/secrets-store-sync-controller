/*
Copyright 2024 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package secretutil

import (
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/pkcs12"
	corev1 "k8s.io/api/core/v1"

	secretsyncv1alpha1 "sigs.k8s.io/secrets-store-sync-controller/api/v1alpha1"
)

const (
	certType          = "CERTIFICATE"
	privateKeyType    = "PRIVATE KEY"
	privateKeyTypeRSA = "RSA PRIVATE KEY"
	privateKeyTypeEC  = "EC PRIVATE KEY"
)

// GetCertPart returns the certificate or the private key part of the cert
func GetCertPart(data []byte, key string) ([]byte, error) {
	if key == corev1.TLSPrivateKeyKey {
		return getPrivateKey(data)
	}
	if key == corev1.TLSCertKey {
		return getCert(data)
	}
	return nil, fmt.Errorf("key '%s' is not supported. Only 'tls.key' and 'tls.crt' are supported", key)
}

// getCert returns the certificate part of a cert
func getCert(data []byte) ([]byte, error) {
	var certs []byte
	for {
		pemBlock, rest := pem.Decode(data)
		if pemBlock == nil {
			break
		}
		if pemBlock.Type == certType {
			block := pem.EncodeToMemory(pemBlock)
			certs = append(certs, block...)
		}
		data = rest
	}

	// if cert is nil, then it might be a pfx cert
	if certs == nil {
		pemBlocks, err := pkcs12.ToPEM(data, "")
		if err != nil {
			return nil, err
		}

		// pem Blocks returns both the certificate and private key types
		for _, block := range pemBlocks {
			// get bytes for certificate
			if block.Type == certType {
				certs = append(certs, pem.EncodeToMemory(block)...)
			}
		}
	}

	return certs, nil
}

// getPrivateKey returns the private key part of a cert in PEM form
func getPrivateKey(data []byte) ([]byte, error) {
	var derKey, rest []byte
	var pemBlock *pem.Block

	for {
		pemBlock, rest = pem.Decode(data)
		if pemBlock == nil {
			break
		}
		if pemBlock.Type != certType {
			break
		}
		data = rest
	}

	// if both der is nil, then certificate might be in the pfx format
	if pemBlock == nil {
		// pkcs12.ToPEM mangles the private key in such a way, that it sets the
		// block to "PRIVATE KEY" which would suggest PKCS#8 format, however
		// the data is either PKCS#1 (RSA) or SEC 1 (ECDSA).
		// This is why we cannot use k8s keyutil.ParsePrivateKeyPEM as it expects
		// properly formatted data. Instead, guess RSA/ECDSA by parsing the DER.
		pemBlocks, err := pkcs12.ToPEM(data, "")
		if err != nil {
			return nil, err
		}

		// pem blocks returns both the certificate and private key types
		for _, block := range pemBlocks {
			// get bytes for private key
			if block.Type == privateKeyType {
				pemBlock = block
				break
			}
		}
	}

	if pemBlock == nil {
		return nil, fmt.Errorf("there were no private keys in the bundle")
	}

	var keyType string
	if pemBlock.Type == privateKeyType { // PRIVATE KEY matches the PKCS#8 format
		// parses an unencrypted private key in PKCS #8, ASN.1 DER form
		if key, err := x509.ParsePKCS8PrivateKey(pemBlock.Bytes); err == nil {
			switch key := key.(type) {
			case *rsa.PrivateKey:
				derKey = x509.MarshalPKCS1PrivateKey(key)
				keyType = privateKeyTypeRSA
			case *ecdsa.PrivateKey:
				derKey, err = x509.MarshalECPrivateKey(key)
				keyType = privateKeyTypeEC
				if err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("unknown private key type found while getting key. Only rsa and ecdsa are supported")
			}
		}
	}

	if len(keyType) == 0 && (pemBlock.Type == privateKeyType || pemBlock.Type == privateKeyTypeRSA) {
		// parses an RSA private key in PKCS #1, ASN.1 DER form
		rsaKey, err := x509.ParsePKCS1PrivateKey(pemBlock.Bytes)
		if err == nil {
			keyType = privateKeyTypeRSA
			derKey = x509.MarshalPKCS1PrivateKey(rsaKey)
		}
	}

	if len(keyType) == 0 && (pemBlock.Type == privateKeyType || pemBlock.Type == privateKeyTypeEC) {
		// parses an EC private key in SEC 1, ASN.1 DER form
		if key, err := x509.ParseECPrivateKey(pemBlock.Bytes); err == nil {
			derKey, err = x509.MarshalECPrivateKey(key)
			if err != nil {
				return nil, err
			}
			keyType = privateKeyTypeEC
		}
	}

	if len(keyType) == 0 {
		return nil, fmt.Errorf("there were no recognized keys in the bundle, only rsa and ecdsa are supported")
	}

	retBlock := &pem.Block{
		Type:  keyType,
		Bytes: derKey,
	}
	return pem.EncodeToMemory(retBlock), nil
}

// GetSecretData gets the object contents from the pods target path and returns a
// map that will be populated in the Kubernetes secret data field
func GetSecretData(secretObjData []secretsyncv1alpha1.SecretObjectData, secretType corev1.SecretType, files map[string][]byte) (map[string][]byte, error) {
	datamap := make(map[string][]byte)
	for _, data := range secretObjData {
		sourcePath := strings.TrimSpace(data.SourcePath)
		dataKey := strings.TrimSpace(data.TargetKey)

		if len(sourcePath) == 0 {
			return datamap, fmt.Errorf("source path in secretObject.data is empty")
		}
		if len(dataKey) == 0 {
			return datamap, fmt.Errorf("target key in secretObject.data is empty")
		}
		content, ok := files[sourcePath]
		if !ok {
			return datamap, fmt.Errorf("file matching sourcePath %s not found in the pod", sourcePath)
		}
		datamap[dataKey] = content
		if secretType == corev1.SecretTypeTLS {
			c, err := GetCertPart(content, dataKey)
			if err != nil {
				return datamap, fmt.Errorf("failed to get cert data for %s: %w", dataKey, err)
			}
			datamap[dataKey] = c
		}
	}
	return datamap, nil
}
