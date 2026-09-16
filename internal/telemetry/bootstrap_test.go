package telemetry

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestNewSDKDisabledAndUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name, disabled, endpoint string
	}{
		{name: "unconfigured"},
		{name: "explicitly disabled", disabled: "true", endpoint: "http://127.0.0.1:4317"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_SDK_DISABLED", tc.disabled)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tc.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
			sdk, err := NewSDK(context.Background(), "test-version")
			if err != nil {
				t.Fatal(err)
			}
			if len(sdk.providers) != 0 {
				t.Fatal("disabled SDK created a provider")
			}
			if err := sdk.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := sdk.Shutdown(context.Background()); err != nil {
				t.Fatalf("second shutdown: %v", err)
			}
		})
	}
}

type shutdownFunc func(context.Context) error

func (f shutdownFunc) Shutdown(ctx context.Context) error {
	return f(ctx)
}

func TestShutdownSharesDeadlineAndRunsOnce(t *testing.T) {
	var calls atomic.Int32
	block := shutdownFunc(func(ctx context.Context) error {
		calls.Add(1)
		<-ctx.Done()
		return ctx.Err()
	})
	sdk := &SDK{providers: []shutdowner{block, block}}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := sdk.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("providers did not share shutdown deadline: %v", elapsed)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("shutdown calls = %d, want 2", got)
	}
	if err := sdk.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second shutdown error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("providers shut down more than once: %d", got)
	}
}

func TestSamplerFromEnvironment(t *testing.T) {
	tests := []struct {
		name, sampler, arg string
		want               trace.SamplingDecision
	}{
		{name: "always off", sampler: "always_off", want: trace.Drop},
		{name: "zero ratio", sampler: "traceidratio", arg: "0", want: trace.Drop},
		{name: "always on", sampler: "parentbased_always_on", want: trace.RecordAndSample},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_SAMPLER", tc.sampler)
			t.Setenv("OTEL_TRACES_SAMPLER_ARG", tc.arg)
			decision := samplerFromEnv().ShouldSample(trace.SamplingParameters{
				TraceID: oteltrace.TraceID{1},
			}).Decision
			if decision != tc.want {
				t.Fatalf("decision = %v, want %v", decision, tc.want)
			}
		})
	}
}

func TestResourceEnvironmentOverridesDefaultServiceName(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "custom-piper")
	res, err := newResource(context.Background(), "test-version")
	if err != nil {
		t.Fatal(err)
	}
	for _, attr := range res.Attributes() {
		if string(attr.Key) == "service.name" {
			if got := attr.Value.AsString(); got != "custom-piper" {
				t.Fatalf("service.name = %q", got)
			}
			return
		}
	}
	t.Fatal("service.name missing")
}

func TestSignalEndpointSelection(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://collector:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	if !endpointSet("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") {
		t.Fatal("trace endpoint not detected")
	}
	if endpointSet("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") {
		t.Fatal("metrics unexpectedly enabled")
	}
}
