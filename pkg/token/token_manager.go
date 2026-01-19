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

// Vendored from kubernetes/pkg/kubelet/token/token_manager.go
//  * tag: v1.25.3,
//  * commit: 53ce79a18ab2665488f7c55c6a1cab8e7a09aced
//  * link: https://github.com/kubernetes/kubernetes/blob/53ce79a18ab2665488f7c55c6a1cab8e7a09aced/pkg/kubelet/token/token_manager.go

// Package token implements a manager of serviceaccount tokens for pods running
// on the node.
package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

const (
	maxTTL    = 24 * time.Hour
	gcPeriod  = time.Minute
	maxJitter = 10 * time.Second
)

// NewManager returns a new token manager.
func NewManager(c clientset.Interface) *Manager {
	// check whether the server supports token requests so we can give a more helpful error message
	supported := false
	once := &sync.Once{}
	tokenRequestsSupported := func() bool {
		once.Do(func() {
			resources, err := c.Discovery().ServerResourcesForGroupVersion("v1")
			if err != nil {
				return
			}
			for idx := range resources.APIResources {
				resource := &resources.APIResources[idx]
				if resource.Name == "serviceaccounts/token" {
					supported = true
					return
				}
			}
		})
		return supported
	}

	m := &Manager{
		getToken: func(name, namespace string, tr *authenticationv1.TokenRequest) (*authenticationv1.TokenRequest, error) {
			if c == nil {
				return nil, errors.New("cannot use TokenManager when kubelet is in standalone mode")
			}
			tokenRequest, err := c.CoreV1().ServiceAccounts(namespace).CreateToken(context.TODO(), name, tr, metav1.CreateOptions{})
			if apierrors.IsNotFound(err) && !tokenRequestsSupported() {
				return nil, fmt.Errorf("the API server does not have TokenRequest endpoints enabled")
			}
			return tokenRequest, err
		},
		cache: make(map[string]*authenticationv1.TokenRequest),
		clock: clock.RealClock{},
	}
	go wait.Forever(m.cleanup, gcPeriod)
	return m
}

// Manager manages service account tokens for pods.
type Manager struct {

	// cacheMutex guards the cache
	cacheMutex sync.RWMutex
	cache      map[string]*authenticationv1.TokenRequest

	// mocked for testing
	getToken func(name, namespace string, tr *authenticationv1.TokenRequest) (*authenticationv1.TokenRequest, error)
	clock    clock.Clock
}

// GetServiceAccountToken gets a service account token for a pod from cache or
// from the TokenRequest API. This process is as follows:
// * Check the cache for the current token request.
// * If the token exists and does not require a refresh, return the current token.
// * Attempt to refresh the token.
// * If the token is refreshed successfully, save it in the cache and return the token.
// * If refresh fails and the old token is still valid, log an error and return the old token.
// * If refresh fails and the old token is no longer valid, return an error
func (m *Manager) GetServiceAccountToken(namespace, name string, tr *authenticationv1.TokenRequest) (*authenticationv1.TokenRequest, error) {
	key := keyFunc(name, namespace, tr)

	ctr, ok := m.get(key)

	if ok && !m.requiresRefresh(ctr) {
		return ctr, nil
	}

	tr, err := m.getToken(name, namespace, tr)
	if err != nil {
		switch {
		case !ok:
			return nil, fmt.Errorf("failed to fetch token: %w", err)
		case m.expired(ctr):
			return nil, fmt.Errorf("token %s expired and refresh failed: %w", key, err)
		default:
			klog.ErrorS(err, "Couldn't update token", "cacheKey", key)
			return ctr, nil
		}
	}

	m.set(key, tr)
	return tr, nil
}

func (m *Manager) cleanup() {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()
	for k, tr := range m.cache {
		if m.expired(tr) {
			delete(m.cache, k)
		}
	}
}

func (m *Manager) get(key string) (*authenticationv1.TokenRequest, bool) {
	m.cacheMutex.RLock()
	defer m.cacheMutex.RUnlock()
	ctr, ok := m.cache[key]
	return ctr, ok
}

func (m *Manager) set(key string, tr *authenticationv1.TokenRequest) {
	m.cacheMutex.Lock()
	defer m.cacheMutex.Unlock()
	m.cache[key] = tr
}

func (m *Manager) expired(t *authenticationv1.TokenRequest) bool {
	return m.clock.Now().After(t.Status.ExpirationTimestamp.Time)
}

// requiresRefresh returns true if the token is older than 80% of its total
// ttl, or if the token is older than 24 hours.
func (m *Manager) requiresRefresh(tr *authenticationv1.TokenRequest) bool {
	if tr.Spec.ExpirationSeconds == nil {
		cpy := tr.DeepCopy()
		cpy.Status.Token = ""
		klog.ErrorS(nil, "Expiration seconds was nil for token request", "tokenRequest", cpy)
		return false
	}
	now := m.clock.Now()
	exp := tr.Status.ExpirationTimestamp.Time
	iat := exp.Add(-1 * time.Duration(*tr.Spec.ExpirationSeconds) * time.Second)

	// #nosec G404: Use of weak random number generator (math/rand instead of crypto/rand)
	jitter := time.Duration(rand.Float64()*maxJitter.Seconds()) * time.Second
	if now.After(iat.Add(maxTTL - jitter)) {
		return true
	}
	// Require a refresh if within 20% of the TTL plus a jitter from the expiration time.
	if now.After(exp.Add(-1*time.Duration((*tr.Spec.ExpirationSeconds*20)/100)*time.Second - jitter)) {
		return true
	}
	return false
}

// keys should be nonconfidential and safe to log
func keyFunc(name, namespace string, tr *authenticationv1.TokenRequest) string {
	var exp int64
	if tr.Spec.ExpirationSeconds != nil {
		exp = *tr.Spec.ExpirationSeconds
	}

	var ref authenticationv1.BoundObjectReference
	if tr.Spec.BoundObjectRef != nil {
		ref = *tr.Spec.BoundObjectRef
	}

	return fmt.Sprintf("%q/%q/%#v/%#v/%#v", name, namespace, tr.Spec.Audiences, exp, ref)
}

// SecretProviderServiceAccountTokenAttrs returns the token for the federated service account that can be bound to the pod.
// This token will be sent to the providers and is of the format:
//
//	"csi.storage.k8s.io/serviceAccount.tokens": {
//	  <audience>: {
//	    'token': <token>,
//	    'expirationTimestamp': <expiration timestamp in RFC3339 format>,
//	  },
//	  ...
//	}
//
// ref: https://kubernetes-csi.github.io/docs/token-requests.html#usage
func SecretProviderServiceAccountTokenAttrs(tokenManager *Manager, namespace, serviceAccountName string, audiences []string) (map[string]string, error) {
	if len(audiences) == 0 {
		return nil, nil
	}

	outputs := map[string]authenticationv1.TokenRequestStatus{}
	var tokenExpirationSeconds int64 = 600

	for _, aud := range audiences {
		tr := &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: &tokenExpirationSeconds,
				Audiences:         []string{aud},
			},
		}

		tr, err := tokenManager.GetServiceAccountToken(namespace, serviceAccountName, tr)
		if err != nil {
			return nil, err
		}
		outputs[aud] = tr.Status
	}

	klog.V(5).InfoS("Fetched service account token attrs", "serviceAccountName", serviceAccountName, "namespace", namespace)
	tokens, err := json.Marshal(outputs)
	if err != nil {
		return nil, err
	}

	return map[string]string{
		"csi.storage.k8s.io/serviceAccount.tokens": string(tokens),
	}, nil
}
