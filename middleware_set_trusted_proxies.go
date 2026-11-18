package httpserver

import (
	"context"
	"fmt"
	"net"

	"github.com/gin-gonic/gin"
)

type trustedProxiesKey struct{}

func NewSetTrustedProxiesMiddleware(trustedProxies []string) (gin.HandlerFunc, error) {
	parsed, err := parseCIDRs(trustedProxies)
	if err != nil {
		return nil, fmt.Errorf("error parsing trusted proxies: %w", err)
	}

	return func(ginCtx *gin.Context) {
		ctx := ginCtx.Request.Context()
		ctx = context.WithValue(ctx, trustedProxiesKey{}, parsed)
		ginCtx.Request = ginCtx.Request.WithContext(ctx)

		ginCtx.Next()
	}, nil
}

func getTrustedProxies(ctx context.Context) []*net.IPNet {
	trustedProxies, ok := ctx.Value(trustedProxiesKey{}).([]*net.IPNet)
	if !ok {
		return nil
	}

	return trustedProxies
}

func parseCIDRs(values []string) ([]*net.IPNet, error) {
	var err error
	var cidr *net.IPNet
	cidrs := make([]*net.IPNet, 0, len(values))

	for _, value := range values {
		if _, cidr, err = net.ParseCIDR(value); err != nil {
			return nil, fmt.Errorf("parse trusted CIDR %q: %w", value, err)
		}

		cidrs = append(cidrs, cidr)
	}

	return cidrs, nil
}
