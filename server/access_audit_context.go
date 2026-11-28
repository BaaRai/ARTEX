package server

import (
	"context"
	"github.com/Autumn-27/artex/accessaudit"
)

func (m *Manager) auditContext(ctx context.Context) context.Context {
	// Standalone engine contexts (including guard checks) need no audit wiring.
	if m == nil {
		return ctx
	}
	ctx = accessaudit.WithStore(ctx, m.audit)
	if m.traffic != nil {
		ctx = accessaudit.WithProxyFactory(ctx, m.traffic.ExecutionProxy)
	}
	return ctx
}
