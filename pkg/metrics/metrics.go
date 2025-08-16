/*
Copyright 2025 The Kubernetes Authors.

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

package metrics

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// factory is a promauto.Factory that registers metrics with the controller-runtime's global registry.
// This is the recommended way to use promauto with controller-runtime, as it ensures
// that your custom metrics are exposed alongside the default controller metrics.
var factory = promauto.With(metrics.Registry)

var (
	// RuntimeOS is the operating system of the controller.
	RuntimeOS = runtime.GOOS

	// ReconcileTotal is a metric for the total number of reconciliation requests.
	ReconcileTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "secrets_store_sync_controller_reconcile_total",
			Help: "Total number of successful reconciliations",
		},
		[]string{},
	)
	// ReconcileErrorsTotal is a metric for the total number of reconciliation errors.
	ReconcileErrorsTotal = factory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "secrets_store_sync_controller_reconcile_errors_total",
			Help: "Total number of reconciliation errors",
		},
		[]string{"ErrorType"},
	)
	// ReconcileDuration is a metric for the duration of reconciliations.
	ReconcileDuration = factory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "secrets_store_sync_controller_reconcile_duration_seconds",
			Help:    "The duration of the reconciliation loop",
			Buckets: prometheus.ExponentialBucketsRange(0.1, 2, 11),
		},
		[]string{},
	)
)
