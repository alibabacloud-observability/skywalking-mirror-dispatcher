// Package proxy implements the typed SkyWalking gRPC relay.
package proxy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/policy"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/telemetry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Proxy struct {
	cfg     config.Config
	oap     targetClients
	arms    targetClients
	metrics *telemetry.Metrics
	logger  *slog.Logger

	rootCtx    context.Context
	cancelRoot context.CancelFunc
	inboundSem chan struct{}
	armsSem    chan struct{}

	stateMu    sync.Mutex
	draining   bool
	incomingWG sync.WaitGroup
	armsWG     sync.WaitGroup
}

func New(cfg config.Config, oap, arms grpc.ClientConnInterface, metrics *telemetry.Metrics, logger *slog.Logger) *Proxy {
	rootCtx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	return &Proxy{
		cfg:        cfg,
		oap:        newTargetClients(oap),
		arms:       newTargetClients(arms),
		metrics:    metrics,
		logger:     logger,
		rootCtx:    rootCtx,
		cancelRoot: cancel,
		inboundSem: make(chan struct{}, cfg.MaxInflightRPCs),
		armsSem:    make(chan struct{}, cfg.ARMSMaxConcurrent),
	}
}

func (p *Proxy) UnaryInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if _, ok := policy.Lookup(info.FullMethod); !ok {
		return handler(ctx, req)
	}
	if !p.enterIncoming() {
		p.logger.Warn("incoming RPC rejected", "method", info.FullMethod, "reason", "draining_or_saturated")
		return nil, status.Error(codes.ResourceExhausted, "skywalking mirror is saturated or draining")
	}
	defer p.leaveIncoming()
	return handler(ctx, req)
}

func (p *Proxy) StreamInterceptor(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	if _, ok := policy.Lookup(info.FullMethod); !ok {
		return handler(srv, stream)
	}
	if !p.enterIncoming() {
		p.logger.Warn("incoming RPC rejected", "method", info.FullMethod, "reason", "draining_or_saturated")
		return status.Error(codes.ResourceExhausted, "skywalking mirror is saturated or draining")
	}
	defer p.leaveIncoming()
	return handler(srv, stream)
}

func (p *Proxy) StartDrain() {
	p.stateMu.Lock()
	p.draining = true
	p.stateMu.Unlock()
}

func (p *Proxy) WaitIncoming(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.incomingWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Proxy) StopARMS() { p.cancelRoot() }

func (p *Proxy) WaitARMS(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.armsWG.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Proxy) enterIncoming() bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	if p.draining {
		return false
	}
	select {
	case p.inboundSem <- struct{}{}:
		p.incomingWG.Add(1)
		return true
	default:
		return false
	}
}

func (p *Proxy) leaveIncoming() {
	<-p.inboundSem
	p.incomingWG.Done()
}

func (p *Proxy) tryStartARMS(method string) (func(), bool) {
	select {
	case p.armsSem <- struct{}{}:
		p.armsWG.Add(1)
		p.metrics.IncInflight("arms")
		return func() {
			p.metrics.DecInflight("arms")
			<-p.armsSem
			p.armsWG.Done()
		}, true
	default:
		p.metrics.ObserveARMS(method, "skipped")
		p.logger.Warn("ARMS mirror skipped", "method", method, "reason", "concurrency_limit")
		return nil, false
	}
}

func (p *Proxy) observeOAP(method string, started time.Time, err error) {
	p.metrics.ObserveOAP(method, status.Code(err), time.Since(started).Seconds())
}
