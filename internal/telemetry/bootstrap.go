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
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
)

const shutdownTimeout = 5 * time.Second

// SDK contains the providers initialized by NewSDK. A zero SDK is safe to use.
type SDK struct {
	providers    []shutdowner
	shutdownOnce sync.Once
	shutdownErr  error
}

type shutdowner interface {
	Shutdown(context.Context) error
}

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

	res, err := newResource(ctx, version)
	if err != nil {
		return nil, err
	}

	var traceProvider *sdktrace.TracerProvider
	if tracesEnabled {
		exporter, err := traceexport.New(ctx)
		if err != nil {
			return nil, err
		}
		traceProvider = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithSampler(samplerFromEnv()),
		)
		sdk.providers = append(sdk.providers, traceProvider)
	}

	var metricProvider *sdkmetric.MeterProvider
	if metricsEnabled {
		exporter, err := metricexport.New(ctx)
		if err != nil {
			if traceProvider != nil {
				bounded, cancel := context.WithTimeout(ctx, shutdownTimeout)
				_ = traceProvider.Shutdown(bounded)
				cancel()
			}
			return nil, err
		}
		metricProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
		)
		sdk.providers = append(sdk.providers, metricProvider)
	}

	if traceProvider != nil {
		otel.SetTracerProvider(traceProvider)
	}
	if metricProvider != nil {
		otel.SetMeterProvider(metricProvider)
	}
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

// Shutdown stops all configured providers within one shared deadline. It is a
// one-shot operation; subsequent calls return the result of the first call.
func (s *SDK) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.shutdownOnce.Do(func() {
		bounded, cancel := context.WithTimeout(ctx, shutdownTimeout)
		defer cancel()

		var errs []error
		for i := len(s.providers) - 1; i >= 0; i-- {
			errs = append(errs, s.providers[i].Shutdown(bounded))
		}
		s.shutdownErr = errors.Join(errs...)
	})
	return s.shutdownErr
}
