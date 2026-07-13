// Package upstream owns OAP and ARMS gRPC connections.
package upstream

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/alibabacloud-observability/skywalking-mirror-dispatcher/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type Connections struct {
	OAP  *grpc.ClientConn
	ARMS *grpc.ClientConn
}

func Dial(cfg config.Config) (*Connections, error) {
	oapCreds, err := oapCredentials(cfg)
	if err != nil {
		return nil, err
	}
	armsCreds := credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12})
	common := []grpc.DialOption{
		grpc.WithDisableRetry(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(cfg.MaxMessageBytes),
			grpc.MaxCallSendMsgSize(cfg.MaxMessageBytes),
		),
	}
	oap, err := grpc.NewClient(cfg.OAPEndpoint, append(common, grpc.WithTransportCredentials(oapCreds))...)
	if err != nil {
		return nil, fmt.Errorf("create OAP client: %w", err)
	}
	arms, err := grpc.NewClient(cfg.ARMSEndpoint, append(common, grpc.WithTransportCredentials(armsCreds))...)
	if err != nil {
		_ = oap.Close()
		return nil, fmt.Errorf("create ARMS client: %w", err)
	}
	return &Connections{OAP: oap, ARMS: arms}, nil
}

func (c *Connections) Close() error {
	if c == nil {
		return nil
	}
	var first error
	if c.OAP != nil {
		first = c.OAP.Close()
	}
	if c.ARMS != nil {
		if err := c.ARMS.Close(); first == nil {
			first = err
		}
	}
	return first
}

func ServerCredentials(cfg config.Config) (credentials.TransportCredentials, bool, error) {
	if cfg.ListenerTLSCertFile == "" {
		return nil, false, nil
	}
	creds, err := credentials.NewServerTLSFromFile(cfg.ListenerTLSCertFile, cfg.ListenerTLSKeyFile)
	if err != nil {
		return nil, false, fmt.Errorf("load listener TLS credentials: %w", err)
	}
	return creds, true, nil
}

func oapCredentials(cfg config.Config) (credentials.TransportCredentials, error) {
	if !cfg.OAPTLS {
		return insecure.NewCredentials(), nil
	}
	if cfg.OAPCAFile == "" {
		return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}), nil
	}
	pem, err := os.ReadFile(cfg.OAPCAFile)
	if err != nil {
		return nil, fmt.Errorf("read OAP CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("OAP_CA_FILE contains no certificates")
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}), nil
}
