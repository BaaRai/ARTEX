package accessaudit

import "context"

// ProxyFactory issues an opaque, revocable capability for this execution. The
// proxy resolves it server-side; client-supplied task headers are never trusted.
type ProxyFactory func(Identity) (string, string, func())
type proxyFactoryKey struct{}

func WithProxyFactory(ctx context.Context, f ProxyFactory) context.Context {
	return context.WithValue(ctx, proxyFactoryKey{}, f)
}
func ExecutionProxy(ctx context.Context, fallback, caCert string) (string, string, func()) {
	if f, ok := ctx.Value(proxyFactoryKey{}).(ProxyFactory); ok && f != nil {
		return f(IdentityFrom(ctx))
	}
	return fallback, caCert, func() {}
}
