// Package storetest bietet ein programmierbares Test-Double für store.Store.
package storetest

import (
	"context"

	"github.com/rocketplaneio/rocketplane/services/query/internal/model"
	"github.com/rocketplaneio/rocketplane/services/query/internal/store"
)

// Fake ist ein steuerbares store.Store-Double für Handler-Tests.
type Fake struct {
	ServicesResult model.ServicesResult
	TraceList      model.TraceList
	TraceDetail    model.TraceDetail

	LogList       model.LogList
	ServiceDetail model.ServiceDetail

	ServicesErr error
	ServiceErr  error
	TracesErr   error
	TraceErr    error
	LogsErr     error
	PingErr     error

	// Aufzeichnungen zur Assertion.
	LastServicesQuery store.ServicesQuery
	LastServiceQuery  store.ServiceQuery
	LastTracesQuery   store.TracesQuery
	LastTraceID       string
	LastLogsQuery     store.LogsQuery
}

var _ store.Store = (*Fake)(nil)

func (f *Fake) Services(_ context.Context, q store.ServicesQuery) (model.ServicesResult, error) {
	f.LastServicesQuery = q
	return f.ServicesResult, f.ServicesErr
}

func (f *Fake) Service(_ context.Context, q store.ServiceQuery) (model.ServiceDetail, error) {
	f.LastServiceQuery = q
	return f.ServiceDetail, f.ServiceErr
}

func (f *Fake) Traces(_ context.Context, q store.TracesQuery) (model.TraceList, error) {
	f.LastTracesQuery = q
	return f.TraceList, f.TracesErr
}

func (f *Fake) Trace(_ context.Context, traceID string) (model.TraceDetail, error) {
	f.LastTraceID = traceID
	return f.TraceDetail, f.TraceErr
}

func (f *Fake) Logs(_ context.Context, q store.LogsQuery) (model.LogList, error) {
	f.LastLogsQuery = q
	return f.LogList, f.LogsErr
}

func (f *Fake) Ping(context.Context) error { return f.PingErr }
func (f *Fake) Close() error               { return nil }
