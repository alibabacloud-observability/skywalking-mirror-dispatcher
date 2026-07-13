// Package app wires listeners, upstream connections, proxy handlers and
// graceful shutdown into one process lifecycle.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/proxy"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/telemetry"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/upstream"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

type App struct {
	cfg    config.Config
	logger *zap.Logger

	connections *upstream.Connections
	proxy       *proxy.Proxy
	grpcServer  *grpc.Server
	adminServer *http.Server
	grpcLis     net.Listener
	adminLis    net.Listener
	ready       atomic.Bool
	stopOnce    sync.Once
	serveErr    chan error
}

func New(cfg config.Config, logger *zap.Logger) (*App, error) {
	if logger == nil {
		logger = zap.NewNop()
	}
	connections, err := upstream.Dial(cfg)
	if err != nil {
		return nil, err
	}
	registry := prometheus.NewRegistry()
	metrics := telemetry.New(registry)
	p := proxy.New(cfg, connections.OAP, connections.ARMS, metrics, logger)

	serverOptions := []grpc.ServerOption{
		grpc.UnaryInterceptor(p.UnaryInterceptor),
		grpc.StreamInterceptor(p.StreamInterceptor),
		grpc.MaxRecvMsgSize(cfg.MaxMessageBytes),
		grpc.MaxSendMsgSize(cfg.MaxMessageBytes),
	}
	creds, enabled, err := upstream.ServerCredentials(cfg)
	if err != nil {
		_ = connections.Close()
		return nil, err
	}
	if enabled {
		serverOptions = append(serverOptions, grpc.Creds(creds))
	}
	grpcServer := grpc.NewServer(serverOptions...)
	p.RegisterServices(grpcServer)

	app := &App{
		cfg:         cfg,
		logger:      logger,
		connections: connections,
		proxy:       p,
		grpcServer:  grpcServer,
		serveErr:    make(chan error, 2),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", app.health)
	mux.HandleFunc("/readyz", app.readiness)
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	app.adminServer = &http.Server{Addr: cfg.AdminAddr, Handler: mux, ReadHeaderTimeout: cfg.ARMSFinishTimeout}
	return app, nil
}

func (a *App) Start() error {
	grpcLis, err := net.Listen("tcp", a.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen for gRPC: %w", err)
	}
	adminLis, err := net.Listen("tcp", a.cfg.AdminAddr)
	if err != nil {
		_ = grpcLis.Close()
		return fmt.Errorf("listen for admin HTTP: %w", err)
	}
	a.grpcLis = grpcLis
	a.adminLis = adminLis
	a.ready.Store(true)
	a.logger.Info("servers started", zap.String("grpc_addr", grpcLis.Addr().String()), zap.String("admin_addr", adminLis.Addr().String()))
	go func() {
		if err := a.grpcServer.Serve(grpcLis); err != nil {
			a.serveErr <- fmt.Errorf("gRPC server: %w", err)
		}
	}()
	go func() {
		if err := a.adminServer.Serve(adminLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.serveErr <- fmt.Errorf("admin server: %w", err)
		}
	}()
	return nil
}

func (a *App) Run(ctx context.Context) error {
	if err := a.Start(); err != nil {
		_ = a.connections.Close()
		return err
	}
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.DrainTimeout)
		defer cancel()
		return a.Shutdown(shutdownCtx)
	case err := <-a.serveErr:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.DrainTimeout)
		defer cancel()
		_ = a.Shutdown(shutdownCtx)
		return err
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	var shutdownErr error
	a.stopOnce.Do(func() {
		a.ready.Store(false)
		a.proxy.StartDrain()
		a.logger.Info("draining servers")
		gracefulDone := make(chan struct{})
		go func() {
			a.grpcServer.GracefulStop()
			close(gracefulDone)
		}()
		select {
		case <-gracefulDone:
		case <-ctx.Done():
			a.grpcServer.Stop()
			shutdownErr = ctx.Err()
		}
		if err := a.proxy.WaitIncoming(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		a.proxy.StopARMS()
		if err := a.proxy.WaitARMS(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		if err := a.adminServer.Shutdown(ctx); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		if err := a.connections.Close(); err != nil && shutdownErr == nil {
			shutdownErr = err
		}
		a.logger.Info("servers stopped")
	})
	return shutdownErr
}

func (a *App) GRPCAddr() string {
	if a.grpcLis == nil {
		return ""
	}
	return a.grpcLis.Addr().String()
}

func (a *App) AdminAddr() string {
	if a.adminLis == nil {
		return ""
	}
	return a.adminLis.Addr().String()
}

func (a *App) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (a *App) readiness(w http.ResponseWriter, _ *http.Request) {
	if !a.ready.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}
