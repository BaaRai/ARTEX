package accessaudit

import "context"

type storeKey struct{}
type identityKey struct{}

func WithStore(ctx context.Context, s *Store) context.Context {
	return context.WithValue(ctx, storeKey{}, s)
}
func WithIdentity(ctx context.Context, f func(*Identity)) context.Context {
	i := IdentityFrom(ctx)
	f(&i)
	return context.WithValue(ctx, identityKey{}, i)
}
func IdentityFrom(ctx context.Context) Identity {
	i, _ := ctx.Value(identityKey{}).(Identity)
	return i
}
func Record(ctx context.Context, e Event) {
	s, _ := ctx.Value(storeKey{}).(*Store)
	e.Identity = IdentityFrom(ctx)
	s.Record(e)
}
