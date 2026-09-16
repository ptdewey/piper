// Package telemetry owns Piper's OpenTelemetry SDK lifecycle and its small,
// privacy-reviewed application telemetry vocabulary.
package telemetry

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	metricexport "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	traceexport "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const shutdownTimeout = 5 * time.Second

// SDK contains the providers initialized by NewSDK. A zero SDK is safe to use.
type SDK struct {
	traceProvider      *sdktrace.TracerProvider
	metricProvider     *sdkmetric.MeterProvider
	previousTrace      oteltrace.TracerProvider
	previousMetric     otelmetric.MeterProvider
	previousPropagator propagation.TextMapPropagator
	shutdownOnce       sync.Once
	shutdownErr        error
}

var (
	lifecycleMu sync.Mutex
	activeSDK   *SDK
)

// NewSDK configures OTLP/gRPC exporters from the standard OTEL environment.
// Each signal is enabled only when its signal-specific endpoint or the common
// OTLP endpoint is set. When the SDK is disabled (or no endpoint is present),
// it does not create providers, exporters, or background goroutines.
func NewSDK(ctx context.Context, version string) (*SDK, error) {
	sdk := &SDK{}
	if sdkDisabled() {
		return sdk, nil
	}

	tracesEnabled := endpointSet("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")
	metricsEnabled := endpointSet("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")
	if !tracesEnabled && !metricsEnabled {
		return sdk, nil
	}

	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()
	if activeSDK != nil {
		return nil, errors.New("OpenTelemetry SDK is already active")
	}

	res, err := newResource(ctx, version)
	if err != nil {
		return nil, err
	}

	if tracesEnabled {
		exporter, err := traceexport.New(ctx)
		if err != nil {
			return nil, err
		}
		sdk.traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(samplerFromEnv()),
		)
	}

	if metricsEnabled {
		exporter, err := metricexport.New(ctx)
		if err != nil {
			if sdk.traceProvider != nil {
				_ = sdk.traceProvider.Shutdown(context.Background())
			}
			return nil, err
		}
		sdk.metricProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		)
	}

	// Publish process-global providers only after every requested signal has
	// initialized successfully, so a partial failure cannot leave a dead global.
	if sdk.traceProvider != nil {
		sdk.previousTrace = otel.GetTracerProvider()
		otel.SetTracerProvider(sdk.traceProvider)
	}
	if sdk.metricProvider != nil {
		sdk.previousMetric = otel.GetMeterProvider()
		otel.SetMeterProvider(sdk.metricProvider)
	}
	sdk.previousPropagator = otel.GetTextMapPropagator()

	// Install both W3C propagators whenever telemetry is active. Baggage is
	// propagated but never copied into Piper's spans or metrics automatically.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	activeSDK = sdk
	return sdk, nil
}

func newResource(ctx context.Context, version string) (*resource.Resource, error) {
	return resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName("piper"), semconv.ServiceVersion(version)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
}

func samplerFromEnv() sdktrace.Sampler {
	ratio := func() sdktrace.Sampler {
		value, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64)
		if err != nil || value < 0 || value > 1 {
			value = 1
		}
		return sdktrace.TraceIDRatioBased(value)
	}
	switch strings.ToLower(os.Getenv("OTEL_TRACES_SAMPLER")) {
	case "always_off":
		return sdktrace.NeverSample()
	case "traceidratio":
		return ratio()
	case "parentbased_always_off":
		return sdktrace.ParentBased(sdktrace.NeverSample())
	case "parentbased_traceidratio":
		return sdktrace.ParentBased(ratio())
	case "always_on":
		return sdktrace.AlwaysSample()
	case "", "parentbased_always_on":
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	default:
		return sdktrace.ParentBased(sdktrace.AlwaysSample())
	}
}

func endpointSet(signal string) bool {
	return os.Getenv(signal) != "" || os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != ""
}

func sdkDisabled() bool {
	disabled, err := strconv.ParseBool(os.Getenv("OTEL_SDK_DISABLED"))
	return err == nil && disabled
}

// Shutdown flushes and stops all initialized providers. It is idempotent and
// enforces an internal upper bound even if the supplied context has no deadline.
func (s *SDK) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.shutdownOnce.Do(func() {
		var errs []error
		if s.metricProvider != nil {
			bounded, cancel := context.WithTimeout(ctx, shutdownTimeout)
			errs = append(errs, s.metricProvider.Shutdown(bounded))
			cancel()
		}
		if s.traceProvider != nil {
			bounded, cancel := context.WithTimeout(ctx, shutdownTimeout)
			errs = append(errs, s.traceProvider.Shutdown(bounded))
			cancel()
		}
		lifecycleMu.Lock()
		if activeSDK == s {
			if s.previousTrace != nil {
				otel.SetTracerProvider(s.previousTrace)
			}
			if s.previousMetric != nil {
				otel.SetMeterProvider(s.previousMetric)
			}
			if s.previousPropagator != nil {
				otel.SetTextMapPropagator(s.previousPropagator)
			}
			activeSDK = nil
		}
		lifecycleMu.Unlock()
		s.shutdownErr = errors.Join(errs...)
	})
	return s.shutdownErr
}
