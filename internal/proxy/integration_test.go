package proxy

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	configv3 "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	commonv3 "skywalking.apache.org/repo/goapi/collect/common/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	managementv3 "skywalking.apache.org/repo/goapi/collect/management/v3"
)

type unaryRecord struct {
	request *managementv3.InstanceProperties
	md      metadata.MD
}

type managementFake struct {
	managementv3.UnimplementedManagementServiceServer
	response      *commonv3.Commands
	err           error
	header        metadata.MD
	trailer       metadata.MD
	release       <-chan struct{}
	waitForCancel bool
	calls         chan unaryRecord
	canceled      chan struct{}
	cancelOnce    sync.Once
}

func newManagementFake(command string) *managementFake {
	return &managementFake{
		response: &commonv3.Commands{Commands: []*commonv3.Command{{Command: command}}},
		calls:    make(chan unaryRecord, 16),
		canceled: make(chan struct{}),
	}
}

func (f *managementFake) ReportInstanceProperties(ctx context.Context, request *managementv3.InstanceProperties) (*commonv3.Commands, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.calls <- unaryRecord{request: request, md: md}
	if len(f.header) > 0 {
		_ = grpc.SetHeader(ctx, f.header)
	}
	if len(f.trailer) > 0 {
		_ = grpc.SetTrailer(ctx, f.trailer)
	}
	if f.waitForCancel {
		<-ctx.Done()
		f.cancelOnce.Do(func() { close(f.canceled) })
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if f.release != nil {
		select {
		case <-f.release:
		case <-ctx.Done():
			f.cancelOnce.Do(func() { close(f.canceled) })
			return nil, status.FromContextError(ctx.Err()).Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return proto.Clone(f.response).(*commonv3.Commands), nil
}

type streamRecord struct {
	traceIDs []string
	md       metadata.MD
}

type traceFake struct {
	agentv3.UnimplementedTraceSegmentReportServiceServer
	command string
	calls   chan streamRecord
}

func newTraceFake(command string) *traceFake {
	return &traceFake{command: command, calls: make(chan streamRecord, 8)}
}

func (f *traceFake) Collect(stream agentv3.TraceSegmentReportService_CollectServer) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	record := streamRecord{md: md}
	for {
		message, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		record.traceIDs = append(record.traceIDs, message.TraceId)
	}
	f.calls <- record
	return stream.SendAndClose(&commonv3.Commands{Commands: []*commonv3.Command{{Command: f.command}}})
}

type configurationFake struct {
	configv3.UnimplementedConfigurationDiscoveryServiceServer
	command string
	calls   chan metadata.MD
}

func newConfigurationFake(command string) *configurationFake {
	return &configurationFake{command: command, calls: make(chan metadata.MD, 4)}
}

func (f *configurationFake) FetchConfigurations(ctx context.Context, _ *configv3.ConfigurationSyncRequest) (*commonv3.Commands, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.calls <- md
	return &commonv3.Commands{Commands: []*commonv3.Command{{Command: f.command}}}, nil
}

type bufServer struct {
	conn *grpc.ClientConn
	stop func()
}

func startBufServer(t *testing.T, register func(*grpc.Server)) bufServer {
	t.Helper()
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///upstream", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return bufServer{conn: conn, stop: func() {
		_ = conn.Close()
		server.Stop()
		_ = listener.Close()
	}}
}

type proxyHarness struct {
	conn     *grpc.ClientConn
	p        *Proxy
	registry *prometheus.Registry
	stop     func()
}

func startProxyHarness(t *testing.T, cfg configForTest, oap, arms grpc.ClientConnInterface) proxyHarness {
	t.Helper()
	actual := testConfig()
	cfg.apply(&actual)
	p, registry := newTestProxy(oap, arms, actual)
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer(
		grpc.UnaryInterceptor(p.UnaryInterceptor),
		grpc.StreamInterceptor(p.StreamInterceptor),
		grpc.MaxRecvMsgSize(actual.MaxMessageBytes),
		grpc.MaxSendMsgSize(actual.MaxMessageBytes),
	)
	p.RegisterServices(server)
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///mirror", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	return proxyHarness{conn: conn, p: p, registry: registry, stop: func() {
		server.Stop()
		p.StartDrain()
		p.StopARMS()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = p.WaitARMS(ctx)
		_ = conn.Close()
		_ = listener.Close()
	}}
}

type configForTest struct {
	maxInflight    int
	armsConcurrent int
	queueSize      int
	finishTimeout  time.Duration
}

func (c configForTest) apply(cfg *config.Config) {
	if c.maxInflight > 0 {
		cfg.MaxInflightRPCs = c.maxInflight
	}
	if c.armsConcurrent > 0 {
		cfg.ARMSMaxConcurrent = c.armsConcurrent
	}
	if c.queueSize > 0 {
		cfg.ARMSStreamQueueSize = c.queueSize
	}
	if c.finishTimeout > 0 {
		cfg.ARMSFinishTimeout = c.finishTimeout
	}
}

func TestUnaryUsesOAPResponseAndIsolatesMetadata(t *testing.T) {
	oapFake := newManagementFake("oap-command")
	oapFake.header = metadata.Pairs("x-oap-header", "header-value")
	oapFake.trailer = metadata.Pairs("x-oap-trailer", "trailer-value")
	armsFake := newManagementFake("arms-command")
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{}, oap.conn, arms.conn)
	defer harness.stop()

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authentication", "oap-token", "x-customer", "customer-value"))
	client := managementv3.NewManagementServiceClient(harness.conn)
	var header, trailer metadata.MD
	response, err := client.ReportInstanceProperties(ctx, &managementv3.InstanceProperties{Service: "checkout", ServiceInstance: "pod-1"}, grpc.Header(&header), grpc.Trailer(&trailer))
	if err != nil {
		t.Fatalf("ReportInstanceProperties() error = %v", err)
	}
	if got := response.Commands[0].Command; got != "oap-command" {
		t.Fatalf("command = %q, want OAP command", got)
	}
	oapCall := receive(t, oapFake.calls)
	armsCall := receive(t, armsFake.calls)
	if oapCall.request.Service != "checkout" || armsCall.request.Service != "checkout" {
		t.Fatalf("request not forwarded: OAP=%+v ARMS=%+v", oapCall.request, armsCall.request)
	}
	if got := oapCall.md.Get("authentication"); len(got) != 1 || got[0] != "oap-token" {
		t.Fatalf("OAP authentication = %v", got)
	}
	if got := oapCall.md.Get("x-customer"); len(got) != 1 || got[0] != "customer-value" {
		t.Fatalf("OAP application metadata = %v", got)
	}
	if got := armsCall.md.Get("authentication"); len(got) != 1 || got[0] != "arms-token" {
		t.Fatalf("ARMS authentication = %v", got)
	}
	if got := armsCall.md.Get("x-customer"); len(got) != 0 {
		t.Fatalf("ARMS received inbound metadata: %v", got)
	}
	if header.Get("x-oap-header")[0] != "header-value" || trailer.Get("x-oap-trailer")[0] != "trailer-value" {
		t.Fatalf("OAP metadata not returned: header=%v trailer=%v", header, trailer)
	}
}

func TestClientStreamPreservesOrderAndOAPCommands(t *testing.T) {
	oapFake := newTraceFake("oap-stream-command")
	armsFake := newTraceFake("arms-stream-command")
	oap := startBufServer(t, func(server *grpc.Server) { agentv3.RegisterTraceSegmentReportServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { agentv3.RegisterTraceSegmentReportServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{}, oap.conn, arms.conn)
	defer harness.stop()

	client := agentv3.NewTraceSegmentReportServiceClient(harness.conn)
	stream, err := client.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, traceID := range []string{"trace-1", "trace-2", "trace-3"} {
		if err := stream.Send(&agentv3.SegmentObject{TraceId: traceID}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Commands[0].Command; got != "oap-stream-command" {
		t.Fatalf("command = %q, want OAP command", got)
	}
	want := []string{"trace-1", "trace-2", "trace-3"}
	if got := receive(t, oapFake.calls).traceIDs; !equalStrings(got, want) {
		t.Fatalf("OAP order = %v, want %v", got, want)
	}
	if got := receive(t, armsFake.calls).traceIDs; !equalStrings(got, want) {
		t.Fatalf("ARMS order = %v, want %v", got, want)
	}
}

func TestControlMethodIsOAPOnly(t *testing.T) {
	oapFake := newConfigurationFake("oap-cds")
	armsFake := newConfigurationFake("arms-cds")
	oap := startBufServer(t, func(server *grpc.Server) { configv3.RegisterConfigurationDiscoveryServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { configv3.RegisterConfigurationDiscoveryServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{}, oap.conn, arms.conn)
	defer harness.stop()

	response, err := configv3.NewConfigurationDiscoveryServiceClient(harness.conn).FetchConfigurations(context.Background(), &configv3.ConfigurationSyncRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.Commands[0].Command != "oap-cds" {
		t.Fatalf("unexpected response: %v", response)
	}
	_ = receive(t, oapFake.calls)
	select {
	case <-armsFake.calls:
		t.Fatal("OAP-only method reached ARMS")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOAPErrorDetailsRemainAuthoritative(t *testing.T) {
	oapFake := newManagementFake("unused")
	detailed, err := status.New(codes.InvalidArgument, "OAP rejected request").WithDetails(&commonv3.Command{Command: "detail"})
	if err != nil {
		t.Fatal(err)
	}
	oapFake.err = detailed.Err()
	oapFake.trailer = metadata.Pairs("x-oap-error", "true")
	armsFake := newManagementFake("arms-success")
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{}, oap.conn, arms.conn)
	defer harness.stop()

	var trailer metadata.MD
	_, callErr := managementv3.NewManagementServiceClient(harness.conn).ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{}, grpc.Trailer(&trailer))
	got := status.Convert(callErr)
	if got.Code() != codes.InvalidArgument || got.Message() != "OAP rejected request" || len(got.Details()) != 1 {
		t.Fatalf("status = %v details=%v", got, got.Details())
	}
	if trailer.Get("x-oap-error")[0] != "true" {
		t.Fatalf("trailer = %v", trailer)
	}
}

func TestARMSBlockingAndSemaphoreSaturationDoNotDelayOAP(t *testing.T) {
	release := make(chan struct{})
	oapFake := newManagementFake("oap")
	armsFake := newManagementFake("arms")
	armsFake.release = release
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{armsConcurrent: 1, finishTimeout: 2 * time.Second}, oap.conn, arms.conn)
	defer harness.stop()
	client := managementv3.NewManagementServiceClient(harness.conn)

	started := time.Now()
	if _, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "first"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("OAP ACK waited for ARMS: %v", elapsed)
	}
	_ = receive(t, armsFake.calls)
	if _, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "second"}); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-armsFake.calls:
		t.Fatalf("second ARMS call unexpectedly started: %+v", record.request)
	case <-time.After(75 * time.Millisecond):
	}
	if metricValue(t, harness.registry, "skywalking_mirror_arms_rpc_total", map[string]string{"method": methodManagementProperties, "result": "skipped"}) != 1 {
		t.Fatal("skipped terminal metric not recorded")
	}
	close(release)
}

func TestAgentCancellationCancelsBothUpstreams(t *testing.T) {
	oapFake := newManagementFake("oap")
	oapFake.waitForCancel = true
	armsFake := newManagementFake("arms")
	armsFake.waitForCancel = true
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{finishTimeout: time.Second}, oap.conn, arms.conn)
	defer harness.stop()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := managementv3.NewManagementServiceClient(harness.conn).ReportInstanceProperties(ctx, &managementv3.InstanceProperties{})
		result <- err
	}()
	_ = receive(t, oapFake.calls)
	_ = receive(t, armsFake.calls)
	cancel()
	if code := status.Code(receive(t, result)); code != codes.Canceled {
		t.Fatalf("client code = %s, want Canceled", code)
	}
	waitClosed(t, oapFake.canceled)
	waitClosed(t, armsFake.canceled)
}

func TestIngressSaturationRejectsBeforeSecondUpstream(t *testing.T) {
	release := make(chan struct{})
	oapFake := newManagementFake("oap")
	oapFake.release = release
	armsFake := newManagementFake("arms")
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{maxInflight: 1}, oap.conn, arms.conn)
	defer harness.stop()
	client := managementv3.NewManagementServiceClient(harness.conn)
	first := make(chan error, 1)
	go func() {
		_, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "first"})
		first <- err
	}()
	_ = receive(t, oapFake.calls)
	_, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "second"})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second call code = %s, want ResourceExhausted", status.Code(err))
	}
	close(release)
	if err := receive(t, first); err != nil {
		t.Fatalf("first call error = %v", err)
	}
}

func TestDrainRejectsNewRPCAndWaitsForInflightOAP(t *testing.T) {
	release := make(chan struct{})
	oapFake := newManagementFake("oap")
	oapFake.release = release
	armsFake := newManagementFake("arms")
	oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
	defer oap.stop()
	arms := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, armsFake) })
	defer arms.stop()
	harness := startProxyHarness(t, configForTest{}, oap.conn, arms.conn)
	defer harness.stop()
	client := managementv3.NewManagementServiceClient(harness.conn)
	first := make(chan error, 1)
	go func() {
		_, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "inflight"})
		first <- err
	}()
	_ = receive(t, oapFake.calls)
	harness.p.StartDrain()
	if _, err := client.ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{Service: "new"}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("new call during drain error = %v", err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	if err := harness.p.WaitIncoming(waitCtx); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("WaitIncoming before release = %v, want deadline", err)
	}
	cancel()
	close(release)
	if err := receive(t, first); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := harness.p.WaitIncoming(waitCtx); err != nil {
		t.Fatalf("WaitIncoming after release = %v", err)
	}
}

type failingClientConn struct{ err error }

func (c failingClientConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	return c.err
}
func (c failingClientConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, c.err
}

func TestARMSFailuresNeverChangeOAPResponse(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.PermissionDenied, codes.Unavailable} {
		t.Run(code.String(), func(t *testing.T) {
			oapFake := newManagementFake("oap")
			oap := startBufServer(t, func(server *grpc.Server) { managementv3.RegisterManagementServiceServer(server, oapFake) })
			defer oap.stop()
			harness := startProxyHarness(t, configForTest{}, oap.conn, failingClientConn{err: status.Error(code, "ARMS failure")})
			defer harness.stop()
			response, err := managementv3.NewManagementServiceClient(harness.conn).ReportInstanceProperties(context.Background(), &managementv3.InstanceProperties{})
			if err != nil || response.Commands[0].Command != "oap" {
				t.Fatalf("response=%v err=%v", response, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := harness.p.WaitARMS(ctx); err != nil {
				t.Fatal(err)
			}
			if metricValue(t, harness.registry, "skywalking_mirror_arms_rpc_total", map[string]string{"method": methodManagementProperties, "result": "failed"}) != 1 {
				t.Fatal("failed terminal metric not recorded")
			}
		})
	}
}

type blockingClientConn struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingClientConn() *blockingClientConn {
	return &blockingClientConn{started: make(chan struct{})}
}

func (c *blockingClientConn) Invoke(ctx context.Context, _ string, _, _ any, _ ...grpc.CallOption) error {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	return ctx.Err()
}

func (c *blockingClientConn) NewStream(ctx context.Context, _ *grpc.StreamDesc, _ string, _ ...grpc.CallOption) (grpc.ClientStream, error) {
	c.once.Do(func() { close(c.started) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestStreamQueueFullDropsOnlyARMS(t *testing.T) {
	oapFake := newTraceFake("oap")
	oap := startBufServer(t, func(server *grpc.Server) { agentv3.RegisterTraceSegmentReportServiceServer(server, oapFake) })
	defer oap.stop()
	blocking := newBlockingClientConn()
	harness := startProxyHarness(t, configForTest{queueSize: 1, finishTimeout: 50 * time.Millisecond}, oap.conn, blocking)
	defer harness.stop()
	stream, err := agentv3.NewTraceSegmentReportServiceClient(harness.conn).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&agentv3.SegmentObject{TraceId: "one"}); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, blocking.started)
	for _, id := range []string{"two", "three"} {
		if err := stream.Send(&agentv3.SegmentObject{TraceId: id}); err != nil {
			t.Fatal(err)
		}
	}
	response, err := stream.CloseAndRecv()
	if err != nil || response.Commands[0].Command != "oap" {
		t.Fatalf("response=%v err=%v", response, err)
	}
	if got := receive(t, oapFake.calls).traceIDs; !equalStrings(got, []string{"one", "two", "three"}) {
		t.Fatalf("OAP messages = %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := harness.p.WaitARMS(ctx); err != nil {
		t.Fatal(err)
	}
	if metricValue(t, harness.registry, "skywalking_mirror_arms_rpc_total", map[string]string{"method": methodTraceCollect, "result": "dropped"}) != 1 {
		t.Fatal("dropped terminal metric not recorded")
	}
}

func receive[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(2 * time.Second):
		var zero T
		t.Fatal("timed out waiting for test event")
		return zero
	}
}

func waitClosed(t *testing.T, channel <-chan struct{}) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancellation")
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func metricValue(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			matched := true
			for label, value := range labels {
				found := false
				for _, pair := range metric.Label {
					if pair.GetName() == label && pair.GetValue() == value {
						found = true
						break
					}
				}
				if !found {
					matched = false
					break
				}
			}
			if matched && metric.Counter != nil {
				return metric.Counter.GetValue()
			}
		}
	}
	return 0
}
