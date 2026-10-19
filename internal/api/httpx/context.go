package httpx

import (
	"context"
	"net/netip"
)

type requestIDKey struct{}

type clientIPKey struct{}

// WithRequestID returns a context carrying the request id.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id set by the API middleware, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// WithClientIP returns a context carrying the resolved client address.
func WithClientIP(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIP returns the client address resolved by the API middleware (honoring trusted
// proxies); the zero Addr when unknown.
func ClientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip
}
