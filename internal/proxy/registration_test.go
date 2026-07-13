package proxy

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/policy"
	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/telemetry"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

const expectedGoAPIVersion = "v0.0.0-20260521015734-5c05525a3cce"

type noopClientConn struct{}

func (noopClientConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	return status.Error(codes.Unavailable, "test upstream unavailable")
}

func (noopClientConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, status.Error(codes.Unavailable, "test upstream unavailable")
}

func testConfig() config.Config {
	return config.Config{
		ListenAddr:          "127.0.0.1:0",
		AdminAddr:           "127.0.0.1:0",
		OAPEndpoint:         "oap:11800",
		ARMSEndpoint:        "arms:443",
		ARMSAuthentication:  "arms-token",
		MaxMessageBytes:     4 * 1024 * 1024,
		MaxInflightRPCs:     8,
		ARMSMaxConcurrent:   4,
		ARMSStreamQueueSize: 4,
		ARMSFinishTimeout:   100 * time.Millisecond,
		DrainTimeout:        time.Second,
	}
}

func newTestProxy(oap, arms grpc.ClientConnInterface, cfg config.Config) (*Proxy, *prometheus.Registry) {
	registry := prometheus.NewRegistry()
	return New(cfg, oap, arms, telemetry.New(registry), nil), registry
}

func TestRegisteredDescriptorsMatchPolicy(t *testing.T) {
	p, _ := newTestProxy(noopClientConn{}, noopClientConn{}, testConfig())
	server := grpc.NewServer()
	p.RegisterServices(server)
	services := server.GetServiceInfo()
	if len(services) != 16 {
		t.Fatalf("service count = %d, want 16", len(services))
	}
	registered := make(map[string]struct{})
	for service, info := range services {
		for _, method := range info.Methods {
			registered["/"+service+"/"+method.Name] = struct{}{}
		}
	}
	if len(registered) != 29 {
		t.Fatalf("RPC count = %d, want 29", len(registered))
	}
	for method := range policy.Methods {
		if _, ok := registered[method]; !ok {
			t.Errorf("policy method not registered: %s", method)
		}
	}
}

func TestGoAPIVersionPinned(t *testing.T) {
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Version}}", "skywalking.apache.org/repo/goapi").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -m: %v: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != expectedGoAPIVersion {
		t.Fatalf("goapi version = %s, want %s", got, expectedGoAPIVersion)
	}
}

func TestUnregisteredProtocolsReturnUnimplemented(t *testing.T) {
	p, _ := newTestProxy(noopClientConn{}, noopClientConn{}, testConfig())
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(p.UnaryInterceptor), grpc.StreamInterceptor(p.StreamInterceptor))
	p.RegisterServices(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///mirror", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	methods := []string{
		"/skywalking.v3.FutureService/collect",
		"/skywalking.v2.TraceSegmentReportService/collect",
		"/ManagementService/reportInstanceProperties",
		"/skywalking.v10.EBPFProfilingService/collect",
	}
	for _, method := range methods {
		err := conn.Invoke(context.Background(), method, &emptypb.Empty{}, &emptypb.Empty{})
		if status.Code(err) != codes.Unimplemented {
			t.Errorf("%s code = %s, want Unimplemented (err=%v)", method, status.Code(err), err)
		}
	}
}
