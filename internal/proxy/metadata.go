package proxy

import (
	"context"
	"strings"

	"google.golang.org/grpc/metadata"
)

func oapContext(ctx context.Context) context.Context {
	incoming, _ := metadata.FromIncomingContext(ctx)
	outgoing := make(metadata.MD, len(incoming))
	for key, values := range incoming {
		key = strings.ToLower(key)
		if reservedMetadata(key) {
			continue
		}
		outgoing[key] = append([]string(nil), values...)
	}
	return metadata.NewOutgoingContext(ctx, outgoing)
}

func (p *Proxy) armsContext(ctx context.Context) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.Pairs("authentication", p.cfg.ARMSAuthentication))
}

func reservedMetadata(key string) bool {
	return strings.HasPrefix(key, ":") ||
		strings.HasPrefix(key, "grpc-") ||
		key == "content-type" ||
		key == "te" ||
		key == "user-agent"
}
