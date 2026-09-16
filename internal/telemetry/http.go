package telemetry

import (
	"context"
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type originalRequestKey struct{}

// HTTPHandler instruments inbound requests without exporting request paths or
// query strings. The application receives the original URL with the span
// context attached; route middleware adds only the registered route pattern.
func HTTPHandler(next http.Handler) http.Handler {
	instrumented := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, sanitized *http.Request) {
		original, ok := sanitized.Context().Value(originalRequestKey{}).(*http.Request)
		if !ok {
			next.ServeHTTP(w, sanitized)
			return
		}
		next.ServeHTTP(w, original.WithContext(sanitized.Context()))
	}), "piper.http.server")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), originalRequestKey{}, r)
		sanitized := r.Clone(ctx)
		urlCopy := *sanitized.URL
		urlCopy.Path = "/"
		urlCopy.Scheme = ""
		urlCopy.Host = ""
		urlCopy.RawPath = ""
		urlCopy.RawQuery = ""
		urlCopy.ForceQuery = false
		urlCopy.Fragment = ""
		sanitized.URL = &urlCopy
		sanitized.Host = "piper"
		sanitized.RemoteAddr = ""
		sanitized.Method = safeHTTPMethod(sanitized.Method)
		sanitized.Header = propagationHeaders(r.Header)
		sanitized.RequestURI = "/"
		instrumented.ServeHTTP(w, sanitized)
	})
}

func propagationHeaders(headers http.Header) http.Header {
	safe := make(http.Header, 3)
	for _, name := range []string{"Traceparent", "Tracestate", "Baggage"} {
		if values := headers.Values(name); len(values) > 0 {
			safe[name] = append([]string(nil), values...)
		}
	}
	return safe
}

func safeHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

// Route attaches a bounded registered route pattern to an active HTTP span and
// its metrics. The pattern must be a source-code constant, never a request path.
func Route(pattern string, next http.Handler) http.Handler {
	return otelhttp.WithRouteTag(pattern, next)
}
