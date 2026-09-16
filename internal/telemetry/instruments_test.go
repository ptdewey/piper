package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSemanticSpanUsesOnlyBoundedValues(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old) })

	_, span := StartSpan(context.Background(), SpanName("https://secret.invalid?q=x"), Provider("https://secret.invalid?q=x"))
	EndSpan(span, Outcome("private outcome"), ErrorType("raw error https://secret.invalid"))

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans", len(spans))
	}
	if spans[0].Name != "piper.unknown" {
		t.Fatalf("span name = %q", spans[0].Name)
	}
	values := map[string]string{}
	for _, attr := range spans[0].Attributes {
		values[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		"piper.provider": "unknown",
		"piper.outcome":  "error", "piper.error.type": "unknown",
	} {
		if got := values[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := spans[0].Status.Description; got != "unknown" {
		t.Fatalf("status description = %q", got)
	}
	if len(spans[0].Events) != 0 {
		t.Fatal("unexpected events could expose raw errors")
	}
}

func TestBoundedAttributeVocabularies(t *testing.T) {
	if got := providerAttr(Provider("user-id")).Value.AsString(); got != "unknown" {
		t.Fatal(got)
	}
	if got := outcomeAttr(Outcome("partial-secret")).Value.AsString(); got != "error" {
		t.Fatal(got)
	}
	if got := errorTypeAttr(ErrorType("a raw error")).Value.AsString(); got != "unknown" {
		t.Fatal(got)
	}
}
