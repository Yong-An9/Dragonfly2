/*
 *     Copyright 2022 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rpc

import (
	"context"
	"sync"

	"github.com/juju/ratelimit"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"d7y.io/dragonfly/v2/internal/dferrors"
)

var (
	// otelUnaryInterceptor is the unary interceptor for tracing.
	otelUnaryInterceptor grpc.UnaryClientInterceptor

	// otelStreamInterceptor is the stream interceptor for tracing.
	otelStreamInterceptor grpc.StreamClientInterceptor

	// interceptorsInitialized is used to ensure that otel interceptors are initialized only once.
	interceptorsInitialized = sync.Once{}
)

// OTEL interceptors must be created once to avoid memory leak,
// refer to https://github.com/open-telemetry/opentelemetry-go-contrib/issues/4226 and
// https://github.com/argoproj/argo-cd/pull/15174.
func ensureOTELInterceptorInitialized() {
	interceptorsInitialized.Do(func() {
		otelUnaryInterceptor = otelgrpc.UnaryClientInterceptor()
		otelStreamInterceptor = otelgrpc.StreamClientInterceptor()
	})
}

// OTELUnaryClientInterceptor returns a new unary client interceptor that traces gRPC requests.
func OTELUnaryClientInterceptor() grpc.UnaryClientInterceptor {
	ensureOTELInterceptorInitialized()
	return otelUnaryInterceptor
}

// OTELStreamClientInterceptor returns a new stream client interceptor that traces gRPC requests.
func OTELStreamClientInterceptor() grpc.StreamClientInterceptor {
	ensureOTELInterceptorInitialized()
	return otelStreamInterceptor
}

// Refresher is the interface for refreshing dynconfig.
type Refresher interface {
	Refresh() error
}

// UnaryClientInterceptor returns a new unary client interceptor that refresh dynconfig addresses when calling error.
func RefresherUnaryClientInterceptor(r Refresher) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		err := invoker(ctx, method, req, reply, cc, opts...)
		if s, ok := status.FromError(err); ok {
			if s.Code() == codes.ResourceExhausted || s.Code() == codes.Unavailable {
				// nolint
				r.Refresh()
			}
		}

		return err
	}
}

// StreamClientInterceptor returns a new stream client interceptor that refresh dynconfig addresses when calling error.
func RefresherStreamClientInterceptor(r Refresher) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if s, ok := status.FromError(err); ok {
			if s.Code() == codes.ResourceExhausted || s.Code() == codes.Unavailable {
				// nolint
				r.Refresh()
			}
		}
		return clientStream, err
	}
}

// RateLimiterInterceptor is the interface for ratelimit interceptor.
type RateLimiterInterceptor struct {
	// tokenBucket is token bucket of ratelimit.
	tokenBucket *ratelimit.Bucket
}

// NewRateLimiterInterceptor returns a RateLimiterInterceptor instance.
func NewRateLimiterInterceptor(qps float64, burst int64) *RateLimiterInterceptor {
	return &RateLimiterInterceptor{
		tokenBucket: ratelimit.NewBucketWithRate(qps, burst),
	}
}

// Limit is the predicate which limits the requests.
func (r *RateLimiterInterceptor) Limit() bool {
	if r.tokenBucket.TakeAvailable(1) == 0 {
		return true
	}

	return false
}

// ConvertErrorUnaryServerInterceptor returns a new unary server interceptor that convert error when trigger custom error.
func ConvertErrorUnaryServerInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	h, err := handler(ctx, req)
	if err != nil {
		return h, dferrors.ConvertDfErrorToGRPCError(err)
	}

	return h, nil
}

// ConvertErrorStreamServerInterceptor returns a new stream server interceptor that convert error when trigger custom error.
func ConvertErrorStreamServerInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if err := handler(srv, ss); err != nil {
		return dferrors.ConvertDfErrorToGRPCError(err)
	}

	return nil
}

// ConvertErrorUnaryClientInterceptor returns a new unary client interceptor that convert error when trigger custom error.
func ConvertErrorUnaryClientInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	if err := invoker(ctx, method, req, reply, cc, opts...); err != nil {
		return dferrors.ConvertGRPCErrorToDfError(err)
	}

	return nil
}

// ConvertErrorStreamClientInterceptor returns a new stream client interceptor that convert error when trigger custom error.
func ConvertErrorStreamClientInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	s, err := streamer(ctx, desc, cc, method, opts...)
	if err != nil {
		return nil, dferrors.ConvertGRPCErrorToDfError(err)
	}

	return s, nil
}
