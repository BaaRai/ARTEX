package agent

import (
	"context"
	"github.com/Autumn-27/artex/accessaudit"
)

func executionProxyEnv(ctx context.Context, agentKey, proxyAddr, caCert string) ([]string, func()) {
	ctx = accessaudit.WithIdentity(ctx, func(i *accessaudit.Identity) {
		if i.Agent == "" {
			i.Agent = agentKey
		}
	})
	proxyAddr, caCert, release := accessaudit.ExecutionProxy(ctx, proxyAddr, caCert)
	return proxyEnv(proxyAddr, caCert), release
}
