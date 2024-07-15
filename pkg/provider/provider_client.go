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

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
	"sigs.k8s.io/secrets-store-csi-driver/pkg/util/runtimeutil"
	"sigs.k8s.io/secrets-store-csi-driver/provider/v1alpha1"
)

// ServiceConfig is used when building CSIDriverProvider clients. The configured
// retry parameters ensures that RPCs will be retried if the underlying
// connection is not ready.
//
// For more details see:
// https://github.com/grpc/grpc/blob/master/doc/service_config.md
const ServiceConfig = `
{
	"methodConfig": [
		{
			"name": [{"service": "v1alpha1.CSIDriverProvider"}],
			"waitForReady": true,
			"retryPolicy": {
				"MaxAttempts": 3,
				"InitialBackoff": "1s",
				"MaxBackoff": "10s",
				"BackoffMultiplier": 1.1,
				"RetryableStatusCodes": [ "UNAVAILABLE" ]
			}
		}
	]
}
`

var (
	// pluginNameRe is the regular expression used to validate plugin names.
	pluginNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{0,30}$`)

	errInvalidProvider       = errors.New("invalid provider")
	errProviderNotFound      = errors.New("provider not found")
	errMissingObjectVersions = errors.New("missing object versions")
)

// PluginClientBuilder builds and stores grpc clients for communicating with
// provider plugins.
type PluginClientBuilder struct {
	clients     map[string]v1alpha1.CSIDriverProviderClient
	conns       map[string]*grpc.ClientConn
	socketPaths []string
	lock        sync.RWMutex
	opts        []grpc.DialOption
}

// NewPluginClientBuilder creates a PluginClientBuilder that will connect to
// plugins in the provided absolute path to a folder. Plugin servers must listen
// to the unix domain socket at:
//
//	<path>/<plugin_name>.sock
//
// where <plugin_name> must match the PluginNameRe regular expression.
//
// Additional grpc dial options can also be set through opts and will be used
// when creating all clients.
func NewPluginClientBuilder(paths []string, opts ...grpc.DialOption) *PluginClientBuilder {
	pcb := &PluginClientBuilder{
		clients:     make(map[string]v1alpha1.CSIDriverProviderClient),
		conns:       make(map[string]*grpc.ClientConn),
		socketPaths: paths,
		lock:        sync.RWMutex{},
		opts: append(opts, []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()), // the interface is only secured through filesystem ACLs
			grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", target)
			}),
			grpc.WithDefaultServiceConfig(ServiceConfig),
		}...,
		),
	}
	return pcb
}

// Get returns a CSIDriverProviderClient for the provider. If an existing client
// is not found a new one will be created and added to the PluginClientBuilder.
func (p *PluginClientBuilder) Get(_ context.Context, provider string) (v1alpha1.CSIDriverProviderClient, error) {
	var out v1alpha1.CSIDriverProviderClient

	// load a client,
	p.lock.RLock()
	out, ok := p.clients[provider]
	p.lock.RUnlock()
	if ok {
		return out, nil
	}
	// client does not exist, create a new one
	if !pluginNameRe.MatchString(provider) {
		return nil, fmt.Errorf("%w: provider %q", errInvalidProvider, provider)
	}

	// check all paths
	socketPath := ""
	for k := range p.socketPaths {
		tryPath := filepath.Join(p.socketPaths[k], provider+".sock")
		if _, err := os.Stat(tryPath); err == nil {
			socketPath = tryPath
			break
		}
		// TODO: This is a workaround for Windows 20H2 issue for os.Stat(). See
		// microsoft/Windows-Containers#97 for details.
		// Once the issue is resolved, the following os.Lstat() is not needed.
		if runtimeutil.IsRuntimeWindows() {
			if _, err := os.Lstat(tryPath); err == nil {
				socketPath = tryPath
				break
			}
		}
	}

	if socketPath == "" {
		return nil, fmt.Errorf("%w: provider %q", errProviderNotFound, provider)
	}

	conn, err := grpc.Dial(
		socketPath,
		p.opts...,
	)
	if err != nil {
		return nil, err
	}
	out = v1alpha1.NewCSIDriverProviderClient(conn)

	p.lock.Lock()
	defer p.lock.Unlock()

	// retry reading from the map in case a concurrent Get(provider) succeeded
	// and added a connection to the map before p.lock.Lock() was acquired.
	if r, ok := p.clients[provider]; ok {
		out = r
	} else {
		p.conns[provider] = conn
		p.clients[provider] = out
	}
	return out, nil
}

// Cleanup closes all underlying connections and removes all clients.
func (p *PluginClientBuilder) Cleanup() {
	p.lock.Lock()
	defer p.lock.Unlock()

	for k := range p.conns {
		if err := p.conns[k].Close(); err != nil {
			klog.ErrorS(err, "error shutting down provider connection", "provider", k)
		}
	}
	p.clients = make(map[string]v1alpha1.CSIDriverProviderClient)
	p.conns = make(map[string]*grpc.ClientConn)
}

// HealthCheck enables periodic healthcheck for configured provider clients by making
// a Version() RPC call. If the provider healthcheck fails, we log an error.
//
// This method blocks until the parent context is canceled during termination.
func (p *PluginClientBuilder) HealthCheck(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.lock.RLock()

			for provider, client := range p.clients {
				c, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				runtimeVersion, err := Version(c, client)
				if err != nil {
					klog.V(5).ErrorS(err, "provider healthcheck failed", "provider", provider)
					continue
				}
				klog.V(5).InfoS("provider healthcheck successful", "provider", provider, "runtimeVersion", runtimeVersion)
			}

			p.lock.RUnlock()
		}
	}
}

// MountContent calls the client's Mount() RPC with helpers to format the
// request and interpret the response.
func MountContent(ctx context.Context, client v1alpha1.CSIDriverProviderClient, attributes, secrets string, oldObjectVersions map[string]string) (map[string]string, map[string][]byte, error) {
	objVersions := make([]*v1alpha1.ObjectVersion, 0, len(oldObjectVersions))
	for obj, version := range oldObjectVersions {
		objVersions = append(objVersions, &v1alpha1.ObjectVersion{Id: obj, Version: version})
	}

	// TODO: permissions should be deprecated from the provider interface
	permissionJSON, err := json.Marshal(os.FileMode(int(0644)))
	if err != nil {
		return nil, nil, err
	}

	req := &v1alpha1.MountRequest{
		Attributes:           attributes,
		Secrets:              secrets,
		Permission:           string(permissionJSON),
		CurrentObjectVersion: objVersions,
		TargetPath:           "/mnt/secrets-store",
	}

	resp, err := client.Mount(ctx, req)
	if err != nil {
		if isMaxRecvMsgSizeError(err) {
			klog.ErrorS(err, "Set --max-call-recv-msg-size to configure larger maximum size in bytes of gRPC response")
		}
		return nil, nil, err
	}
	klog.V(5).Info("finished mount request")
	if resp != nil && resp.GetError() != nil && len(resp.GetError().Code) > 0 {
		return nil, nil, fmt.Errorf("mount request failed with provider error code %s", resp.GetError().Code)
	}

	ov := resp.GetObjectVersion()
	if ov == nil {
		return nil, nil, errMissingObjectVersions
	}
	objectVersions := make(map[string]string)
	for _, v := range ov {
		objectVersions[v.Id] = v.Version
	}

	// warn if the proto response size is over 1 MiB.
	// Individual k8s secrets are limited to 1MiB in size.
	// Ref: https://kubernetes.io/docs/concepts/configuration/secret/#restrictions
	if size := proto.Size(resp); size > 1048576 {
		klog.InfoS("proto above 1MiB, secret sync may fail", "size", size)
	}

	files := make(map[string][]byte, len(resp.GetFiles()))
	for _, f := range resp.GetFiles() {
		files[f.GetPath()] = f.GetContents()
	}
	return objectVersions, files, nil
}

// Version calls the client's Version() RPC
// returns provider runtime version and error.
func Version(ctx context.Context, client v1alpha1.CSIDriverProviderClient) (string, error) {
	req := &v1alpha1.VersionRequest{
		Version: "v1alpha1",
	}

	resp, err := client.Version(ctx, req)
	if err != nil {
		return "", err
	}
	return resp.RuntimeVersion, nil
}

// isMaxRecvMsgSizeError checks if the grpc error is of ResourceExhausted type and
// msg size is larger than max configured.
func isMaxRecvMsgSizeError(err error) bool {
	if status.Code(err) != codes.ResourceExhausted {
		return false
	}
	// ResourceExhausted errors are not exclusively related to --max-call-recv-msg-size and could also be the result of propagating quota errors.
	// Skipping errors that are related to the machine limits
	if strings.Contains(err.Error(), "grpc: received message larger than max length allowed on current machine") {
		return false
	}
	// Skipping ResourceExhausted errors that are other than internal grpc system errors
	if !strings.Contains(err.Error(), "grpc: received message larger than max") {
		return false
	}
	return true
}
