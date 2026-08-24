/*
Copyright The Kubernetes Authors.

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

package app

import (
	"time"

	"github.com/spf13/pflag"
)

type ControllerOptions struct {
	KubeConfigPath          string
	LeaderElectionNamespace string

	ServingAddress      string
	GRPCResponseMaxSize int

	TokenRequestAudiences []string
	ProviderVolumePath    string
	RotationPollInterval  time.Duration
}

func NewControllerOptions() *ControllerOptions {
	opts := &ControllerOptions{
		LeaderElectionNamespace: "kube-system",

		ServingAddress:      ":8081",
		GRPCResponseMaxSize: 1024 * 1024 * 4,

		ProviderVolumePath:   "/provider",
		RotationPollInterval: 12 * time.Hour,
	}
	return opts
}

func (o *ControllerOptions) AddFlags(flagset *pflag.FlagSet) {
	flagset.StringVar(&o.KubeConfigPath, "kubeconfig", o.KubeConfigPath, "Location of the master configuration file to run from.")
	flagset.StringVar(&o.LeaderElectionNamespace, "leader-election-namespace", o.LeaderElectionNamespace, "Namespace for leader election. Defaults to \"kube-system\".")

	flagset.StringVar(&o.ServingAddress, "controller-server-address", o.ServingAddress, "The address where the controller serves its /healthz and /metrics endpoints.")
	flagset.IntVar(&o.GRPCResponseMaxSize, "max-call-recv-msg-size", o.GRPCResponseMaxSize, "maximum size in bytes of gRPC response from plugins")

	flagset.StringSliceVar(&o.TokenRequestAudiences, "token-request-audience", o.TokenRequestAudiences, "Audiences for the token request, can be specified more than once.")
	flagset.StringVar(&o.ProviderVolumePath, "provider-volume", o.ProviderVolumePath, "Volume path for provider.")
	flagset.DurationVar(&o.RotationPollInterval, "rotation-poll-interval", o.RotationPollInterval, "Polling interval to resync secrets from the provider. Defaults to 12h. To disable provider polling, set it to 0s.")
}
