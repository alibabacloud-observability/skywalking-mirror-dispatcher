package app

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
)

func TestHealthReadyAndBoundedShutdownWithoutUpstreams(t *testing.T) {
	cfg := config.Config{
		ListenAddr:          "127.0.0.1:0",
		AdminAddr:           "127.0.0.1:0",
		OAPEndpoint:         "127.0.0.1:1",
		ARMSEndpoint:        "127.0.0.1:1",
		ARMSAuthentication:  "token",
		MaxMessageBytes:     4 << 20,
		MaxInflightRPCs:     4,
		ARMSMaxConcurrent:   2,
		ARMSStreamQueueSize: 2,
		ARMSFinishTimeout:   50 * time.Millisecond,
		DrainTimeout:        time.Second,
	}
	service, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"/healthz": "ok\n", "/readyz": "ready\n"} {
		response, err := http.Get("http://" + service.AdminAddr() + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || string(body) != want {
			t.Fatalf("%s status=%d body=%q", path, response.StatusCode, body)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := service.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledRunDrainsAndReturns(t *testing.T) {
	cfg := config.Config{
		ListenAddr:          "127.0.0.1:0",
		AdminAddr:           "127.0.0.1:0",
		OAPEndpoint:         "127.0.0.1:1",
		ARMSEndpoint:        "127.0.0.1:1",
		ARMSAuthentication:  "token",
		MaxMessageBytes:     4 << 20,
		MaxInflightRPCs:     4,
		ARMSMaxConcurrent:   2,
		ARMSStreamQueueSize: 2,
		ARMSFinishTimeout:   50 * time.Millisecond,
		DrainTimeout:        time.Second,
	}
	service, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Run(ctx); err != nil {
		t.Fatal(err)
	}
}
