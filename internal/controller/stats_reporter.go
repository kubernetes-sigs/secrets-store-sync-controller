package controller

import (
	"context"
	"regexp"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	scope = "sigs.k8s.io/secrets-store-sync-controller"

	// Validating Admission Policy names that are monitered by metrics
	CreateUpdateVAP               = "secrets-store-sync-controller-create-update-policy"
	CreateUpdateTokenDenyVAP      = "secrets-store-sync-controller-create-update-token-deny-policy"
	UpdateLabelVAP                = "secrets-store-sync-controller-update-label-policy"
	UpdateOwnersCheckOldObjectVAP = "secrets-store-sync-controller-update-owners-check-oldobject-policy"
	ValidateTokenVAP              = "secrets-store-sync-controller-validate-token-policy"
)

var (
	wasSuccessfulKey = attribute.Key("successful")
	namespaceKey     = attribute.Key("namespace")
)

type reporter struct {
	rotationReconcileTotal                metric.Int64Counter
	rotationReconcileDuration             metric.Float64Histogram
	secretSyncDuration                    metric.Float64Histogram
	SSCCreateUpdateVAPTotal               metric.Int64Counter
	SSCCreateUpdateTokenDenyVAPTotal      metric.Int64Counter
	SSCUpdateOwnersCheckOldObjectVAPTotal metric.Int64Counter
	SSCUpdateLabelVAPTotal                metric.Int64Counter
	SSCValidateTokenVAPTotal              metric.Int64Counter
}

type StatsReporter interface {
	ReportRotationReconcile(ctx context.Context, wasSuccessful bool, namespace string)
	ReportRotationReconcileDuration(ctx context.Context, duration float64, wasSuccessful bool, namespace string)
	ReportSecretSyncDuration(ctx context.Context, duration float64, wasSuccessful bool, namespace string)
	ReportVAPDenyMetric(ctx context.Context, err string, namespace string)
}

func NewStatsReporter() (StatsReporter, error) {
	var err error

	r := &reporter{}
	meter := otel.Meter(scope)

	if r.rotationReconcileTotal, err = meter.Int64Counter("rotation_reconcile_total", metric.WithDescription("Total number of rotation reconciles")); err != nil {
		return nil, err
	}

	if r.rotationReconcileDuration, err = meter.Float64Histogram("rotation_reconcile_duration_sec", metric.WithDescription("Distribution of how long it took to rotate secrets-store content for pods")); err != nil {
		return nil, err
	}

	if r.secretSyncDuration, err = meter.Float64Histogram("secrets_store_sync_duration_sec", metric.WithDescription("Distribution of how long it took to sync k8s secret")); err != nil {
		return nil, err
	}

	if r.SSCCreateUpdateVAPTotal, err = meter.Int64Counter("create_update_vap_total", metric.WithDescription("Total number of secrets-store-sync-controller-create-update-policy denials")); err != nil {
		return nil, err
	}

	if r.SSCCreateUpdateTokenDenyVAPTotal, err = meter.Int64Counter("create_update_token_deny_vap_total", metric.WithDescription("Total number of secrets-store-sync-controller-create-update-token-deny-policy denials")); err != nil {
		return nil, err
	}

	if r.SSCUpdateLabelVAPTotal, err = meter.Int64Counter("update_label_vap_total", metric.WithDescription("Total number of secrets-store-sync-controller-update-label-policy denials")); err != nil {
		return nil, err
	}

	if r.SSCUpdateOwnersCheckOldObjectVAPTotal, err = meter.Int64Counter("update_owners_check_oldobject_vap_total", metric.WithDescription("Total number of secrets-store-sync-controller-update-owners-check-oldobject-policy denials")); err != nil {
		return nil, err
	}

	if r.SSCValidateTokenVAPTotal, err = meter.Int64Counter("validate_token_vap_total", metric.WithDescription("Total number of secrets-store-sync-controller-validate-token-policy denials")); err != nil {
		return nil, err
	}

	return r, nil
}

func (r *reporter) ReportRotationReconcileDuration(ctx context.Context, duration float64, wasSuccessful bool, namespace string) {
	options := metric.WithAttributes(
		wasSuccessfulKey.Bool(wasSuccessful),
		namespaceKey.String(namespace),
	)
	r.rotationReconcileDuration.Record(ctx, duration, options)
}

func (r *reporter) ReportRotationReconcile(ctx context.Context, wasSuccessful bool, namespace string) {
	options := metric.WithAttributes(
		wasSuccessfulKey.Bool(wasSuccessful),
		namespaceKey.String(namespace),
	)
	r.rotationReconcileTotal.Add(ctx, 1, options)
}

func (r *reporter) ReportSecretSyncDuration(ctx context.Context, duration float64, wasSuccessful bool, namespace string) {
	options := metric.WithAttributes(
		wasSuccessfulKey.Bool(wasSuccessful),
		namespaceKey.String(namespace),
	)
	r.secretSyncDuration.Record(ctx, duration, options)
}

func (r *reporter) ReportVAPDenyMetric(ctx context.Context, err string, namespace string) {
	logger := log.FromContext(ctx)
	pattern := regexp.MustCompile(`ValidatingAdmissionPolicy '([^']*)'`)
	matches := pattern.FindStringSubmatch(err)

	options := metric.WithAttributes(
		namespaceKey.String(namespace),
	)

	if len(matches) > 1 {
		switch matches[1] {
		case CreateUpdateVAP:
			r.SSCCreateUpdateVAPTotal.Add(ctx, 1, options)

		case CreateUpdateTokenDenyVAP:
			r.SSCCreateUpdateTokenDenyVAPTotal.Add(ctx, 1, options)

		case UpdateLabelVAP:
			r.SSCUpdateLabelVAPTotal.Add(ctx, 1, options)

		case UpdateOwnersCheckOldObjectVAP:
			r.SSCUpdateOwnersCheckOldObjectVAPTotal.Add(ctx, 1, options)

		case ValidateTokenVAP:
			r.SSCValidateTokenVAPTotal.Add(ctx, 1, options)

		default:
			logger.V(5).Info("VAP's metric not implemented. VAP: ", matches[1])
		}
	} else {
		logger.V(5).Info("Unable to parse VAP name from error: ", err)
	}
}
