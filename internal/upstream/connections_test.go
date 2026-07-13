package upstream

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestOAPAndARMSDefaultToPlaintext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthv1.HealthCheckResponse_SERVING)
	healthv1.RegisterHealthServer(server, healthServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	cfg := config.Config{
		OAPEndpoint:        listener.Addr().String(),
		ARMSEndpoint:       listener.Addr().String(),
		ARMSAuthentication: "token",
		MaxMessageBytes:    4 << 20,
	}
	connections, err := Dial(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connections.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := healthv1.NewHealthClient(connections.OAP).Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatalf("plaintext OAP health check failed: %v", err)
	}
	if _, err := healthv1.NewHealthClient(connections.ARMS).Check(ctx, &healthv1.HealthCheckRequest{}); err != nil {
		t.Fatalf("plaintext ARMS health check failed: %v", err)
	}
}

func TestARMSTLSRejectsPlaintextServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthv1.RegisterHealthServer(server, health.NewServer())
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	cfg := config.Config{
		OAPEndpoint:        listener.Addr().String(),
		ARMSEndpoint:       listener.Addr().String(),
		ARMSAuthentication: "token",
		ARMSTLS:            true,
		MaxMessageBytes:    4 << 20,
	}
	connections, err := Dial(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connections.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = healthv1.NewHealthClient(connections.ARMS).Check(ctx, &healthv1.HealthCheckRequest{})
	if err == nil || status.Code(err) != codes.Unavailable {
		t.Fatalf("ARMS TLS against plaintext server error = %v, want Unavailable", err)
	}
}

func TestOAPCustomCAValidation(t *testing.T) {
	file := t.TempDir() + "/invalid-ca.pem"
	if err := os.WriteFile(file, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		OAPEndpoint:        "oap:443",
		OAPTLS:             true,
		OAPCAFile:          file,
		ARMSEndpoint:       "arms:443",
		ARMSAuthentication: "token",
		MaxMessageBytes:    4 << 20,
	}
	if _, err := Dial(cfg); err == nil {
		t.Fatal("Dial() accepted invalid OAP CA")
	}
}
