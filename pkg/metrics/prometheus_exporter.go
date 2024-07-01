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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	otlp "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

func initPrometheusExporter() error {
	exporter, err := otlp.New(
		otlp.WithRegisterer(metrics.Registry.(*prometheus.Registry)), // using the controller-runtime prometheus metrics registry
	)

	if err != nil {
		return err
	}

	meterProvider := metric.NewMeterProvider(
		metric.WithReader(exporter),
		metric.WithView(metric.NewView(
			metric.Instrument{Kind: metric.InstrumentKindHistogram},
			metric.Stream{
				Aggregation: metric.AggregationExplicitBucketHistogram{
					Boundaries: prometheus.ExponentialBucketsRange(0.1, 2, 11),
				}},
		)),
	)

	otel.SetMeterProvider(meterProvider)

	return nil
}
