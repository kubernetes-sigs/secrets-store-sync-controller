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

package version

import (
	"encoding/json"
	"fmt"
	"runtime"
)

var (
	// Vcs is the commit hash for the binary build
	Vcs string
	// BuildTime is the date for the binary build
	BuildTime string
	// BuildVersion is the secrets-store-sync-controller version. Will be overwritten from build.
	BuildVersion = "local-dev"
)

// GetUserAgent returns a user agent of the format: secrets-store-sync-controller/<controller name>/<version> (<goos>/<goarch>) <vcs>/<timestamp>
func GetUserAgent(controllerName string) string {
	return fmt.Sprintf("secrets-store-sync-controller/%s/%s (%s/%s) %s/%s", controllerName, BuildVersion, runtime.GOOS, runtime.GOARCH, Vcs, BuildTime)
}

// PrintVersion prints the current secrets store sync controller version
func PrintVersion() error {
	var err error
	printVersion := struct {
		BuildVersion string
		GitCommit    string
		BuildDate    string
	}{
		BuildDate:    BuildTime,
		BuildVersion: BuildVersion,
		GitCommit:    Vcs,
	}

	var res []byte
	if res, err = json.Marshal(printVersion); err != nil {
		return err
	}

	fmt.Printf(string(res) + "\n")
	return nil
}
