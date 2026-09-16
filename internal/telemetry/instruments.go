package telemetry

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/teal-fm/piper/internal/telemetry"

type SpanName string

const (
	SpanPollCycle          SpanName = "piper.poll.cycle"
	SpanPollAccount        SpanName = "piper.poll.account"
	SpanProviderFetch      SpanName = "piper.provider.fetch"
	SpanMusicBrainzHydrate SpanName = "piper.musicbrainz.hydrate"
	SpanDBPersistPlay      SpanName = "piper.db.persist_play"
	SpanATProtoPublishPlay SpanName = "piper.atproto.publish_play"
)

type Provider string

const (
	ProviderUnknown      Provider = "unknown"
	ProviderListenBrainz Provider = "listenbrainz"
)

type Outcome string

const (
	OutcomeSuccess   Outcome = "success"
	OutcomeError     Outcome = "error"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeEmpty     Outcome = "empty"
)

type ErrorType string

const (
	ErrorTimeout ErrorType = "timeout"
	ErrorUnknown ErrorType = "unknown"
)

type providerContextKey struct{}

// WithProvider carries a bounded provider classification across shared
// MusicBrainz, persistence, and ATProto boundaries.
func WithProvider(ctx context.Context, provider Provider) context.Context {
	return context.WithValue(ctx, providerContextKey{}, validProvider(provider))
}

// ProviderFromContext returns the bounded provider associated with ctx.
func ProviderFromContext(ctx context.Context) Provider {
	provider, ok := ctx.Value(providerContextKey{}).(Provider)
	if !ok {
		return ProviderUnknown
	}
	return validProvider(provider)
}

// Instruments is the shared, low-cardinality metric catalog.
type Instruments struct {
	PollCycles        metric.Int64Counter
	PollCycleDuration metric.Float64Histogram
	PollAccounts      metric.Int64Counter
	PollLastSuccess   metric.Int64Gauge
	PlayPublications  metric.Int64Counter
}

var (
	defaultInstrumentsOnce sync.Once
	defaultInstruments     *Instruments
)

// DefaultInstruments returns the process-wide semantic metric instruments.
// A nil result means the OTel API rejected an instrument definition.
func DefaultInstruments() *Instruments {
	defaultInstrumentsOnce.Do(func() {
		instruments, err := NewInstruments()
		if err == nil {
			defaultInstruments = instruments
		}
	})
	return defaultInstruments
}

func NewInstruments() (*Instruments, error) {
	m := otel.Meter(instrumentationName)
	i := &Instruments{}
	var errs []error
	i.PollCycles, errs = counter(m, "piper.poll.cycle", "{cycle}", errs)
	var err error
	i.PollCycleDuration, err = m.Float64Histogram("piper.poll.cycle.duration", metric.WithUnit("s"))
	errs = append(errs, err)
	i.PollAccounts, errs = counter(m, "piper.poll.account", "{account}", errs)
	i.PollLastSuccess, err = m.Int64Gauge("piper.poll.last_success", metric.WithUnit("s"))
	errs = append(errs, err)
	i.PlayPublications, errs = counter(m, "piper.play.publication", "{play}", errs)
	return i, errors.Join(errs...)
}

func counter(m metric.Meter, name, unit string, errs []error) (metric.Int64Counter, []error) {
	v, err := m.Int64Counter(name, metric.WithUnit(unit))
	return v, append(errs, err)
}

func (i *Instruments) RecordPollCycle(ctx context.Context, provider Provider, outcome Outcome, duration time.Duration) {
	opts := metric.WithAttributes(providerAttr(provider), outcomeAttr(outcome))
	i.PollCycles.Add(ctx, 1, opts)
	i.PollCycleDuration.Record(ctx, duration.Seconds(), opts)
	if outcome == OutcomeSuccess || outcome == OutcomeEmpty {
		i.PollLastSuccess.Record(ctx, time.Now().Unix(), metric.WithAttributes(providerAttr(provider)))
	}
}

func (i *Instruments) RecordPollAccount(ctx context.Context, provider Provider, outcome Outcome) {
	i.PollAccounts.Add(ctx, 1, metric.WithAttributes(providerAttr(provider), outcomeAttr(outcome)))
}

func (i *Instruments) RecordPublication(ctx context.Context, provider Provider, outcome Outcome) {
	i.PlayPublications.Add(ctx, 1, metric.WithAttributes(providerAttr(provider), outcomeAttr(outcome)))
}

// StartSpan starts one of Piper's semantic spans without attaching identifying data.
func StartSpan(ctx context.Context, name SpanName, provider Provider) (context.Context, trace.Span) {
	if provider == "" {
		provider = ProviderFromContext(ctx)
	}
	return otel.Tracer(instrumentationName).Start(ctx, string(validSpanName(name)), trace.WithAttributes(providerAttr(provider)))
}

// EndSpan records only bounded classifications; it deliberately does not accept
// or record an error value because errors can contain URLs and private data.
func EndSpan(span trace.Span, outcome Outcome, errorType ErrorType) {
	outcome = validOutcome(outcome)
	span.SetAttributes(outcomeAttr(outcome))
	if outcome == OutcomeError {
		span.SetAttributes(errorTypeAttr(errorType))
		span.SetStatus(codes.Error, string(validErrorType(errorType)))
	}
	span.End()
}

// ClassifyError maps an error to bounded telemetry values without retaining or
// exporting its text.
func ClassifyError(err error) (Outcome, ErrorType) {
	if err == nil {
		return OutcomeSuccess, ""
	}
	if errors.Is(err, context.Canceled) {
		return OutcomeCancelled, ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeError, ErrorTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return OutcomeError, ErrorTimeout
	}
	return OutcomeError, ErrorUnknown
}

func providerAttr(v Provider) attribute.KeyValue {
	return attribute.String("piper.provider", string(validProvider(v)))
}
func outcomeAttr(v Outcome) attribute.KeyValue {
	return attribute.String("piper.outcome", string(validOutcome(v)))
}
func errorTypeAttr(v ErrorType) attribute.KeyValue {
	return attribute.String("piper.error.type", string(validErrorType(v)))
}
func validProvider(v Provider) Provider {
	switch v {
	case ProviderListenBrainz:
		return v
	}
	return ProviderUnknown
}
func validOutcome(v Outcome) Outcome {
	switch v {
	case OutcomeSuccess, OutcomeError, OutcomeCancelled, OutcomeEmpty:
		return v
	}
	return OutcomeError
}
func validErrorType(v ErrorType) ErrorType {
	switch v {
	case ErrorTimeout:
		return v
	}
	return ErrorUnknown
}

func validSpanName(v SpanName) SpanName {
	switch v {
	case SpanPollCycle, SpanPollAccount, SpanProviderFetch, SpanMusicBrainzHydrate,
		SpanDBPersistPlay, SpanATProtoPublishPlay:
		return v
	}
	return SpanName("piper.unknown")
}
