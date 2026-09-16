package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestHTTPHandlerDoesNotExportRequestSecrets(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(exporter))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(old) })

	const secret = "seeded-private-value"
	mux := http.NewServeMux()
	mux.Handle("/callback", Route("/callback", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("code"); got != secret {
			t.Errorf("handler code = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	req := httptest.NewRequest(http.MethodGet, "https://piper.test/callback?code="+secret+"&state="+secret, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("User-Agent", secret)
	req.Header.Set("X-Forwarded-For", secret)
	req.Host = secret + ".invalid"
	req.RemoteAddr = secret + ":1234"
	req.AddCookie(&http.Cookie{Name: "session", Value: secret})
	res := httptest.NewRecorder()
	HTTPHandler(mux).ServeHTTP(res, req)

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d", res.Code)
	}
	foundRoute := false
	for _, attr := range spans[0].Attributes {
		value := attr.Value.Emit()
		if strings.Contains(value, secret) {
			t.Fatalf("private request data exported in %s=%q", attr.Key, value)
		}
		if string(attr.Key) == "http.route" && value == "/callback" {
			foundRoute = true
		}
	}
	if strings.Contains(spans[0].Name, secret) || strings.Contains(spans[0].Status.Description, secret) {
		t.Fatal("private request data exported in span metadata")
	}
	for _, event := range spans[0].Events {
		if strings.Contains(event.Name, secret) {
			t.Fatal("private request data exported in span event")
		}
		for _, attr := range event.Attributes {
			if strings.Contains(attr.Value.Emit(), secret) {
				t.Fatal("private request data exported in span event attribute")
			}
		}
	}
	if !foundRoute {
		t.Fatal("registered route pattern was not exported")
	}
}
