// Package grpcclient creates connections for service-to-service gRPC calls.
package grpcclient

import (
	"context"

	"github.com/CMOT/habyta/backend/internal/platform/requestid"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// round_robin spreads calls across every address DNS returns for target, so
// use "dns:///<host>:<port>" targets when a service has several replicas.
const serviceConfig = `{"loadBalancingConfig": [{"round_robin": {}}]}`

// New returns a lazily connecting client for target that propagates request
// IDs. Transport is plaintext until mTLS is configured.
func New(target string) (*grpc.ClientConn, error) {
	return grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(serviceConfig),
		grpc.WithChainUnaryInterceptor(propagateRequestID),
	)
}

func propagateRequestID(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if id := requestid.From(ctx); id != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, requestid.MetadataKey, id)
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}
