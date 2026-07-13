package proxy

import (
	"context"
	"io"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type unaryCall[Request, Response any] func(context.Context, Request, ...grpc.CallOption) (Response, error)

func relayUnary[Request, Response any](
	p *Proxy,
	ctx context.Context,
	method string,
	request Request,
	oapCall unaryCall[Request, Response],
	armsCall unaryCall[Request, Response],
	mirror bool,
) (Response, error) {
	var cancelARMS context.CancelFunc
	if mirror {
		cancelARMS = startUnaryMirror(p, method, request, armsCall)
	}

	var header, trailer metadata.MD
	started := time.Now()
	p.metrics.IncInflight("oap")
	response, err := oapCall(oapContext(ctx), request, grpc.Header(&header), grpc.Trailer(&trailer))
	p.metrics.DecInflight("oap")
	p.observeOAP(method, started, err)
	if len(header) > 0 {
		_ = grpc.SetHeader(ctx, header)
	}
	if len(trailer) > 0 {
		_ = grpc.SetTrailer(ctx, trailer)
	}
	if err != nil && cancelARMS != nil {
		cancelARMS()
	}
	return response, err
}

func startUnaryMirror[Request, Response any](p *Proxy, method string, request Request, call unaryCall[Request, Response]) context.CancelFunc {
	release, ok := p.tryStartARMS(method)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(p.rootCtx, p.cfg.ARMSFinishTimeout)
	ctx = p.armsContext(ctx)
	go func() {
		defer release()
		_, err := call(ctx, request)
		if err != nil {
			p.metrics.ObserveARMS(method, "failed")
			p.logger.Warn("ARMS mirror failed", zap.String("method", method), zap.String("code", status.Code(err).String()))
			return
		}
		p.metrics.ObserveARMS(method, "succeeded")
	}()
	return cancel
}

type inboundClientStream[Request, Response any] interface {
	Recv() (Request, error)
	SendAndClose(Response) error
	grpc.ServerStream
}

type outboundClientStream[Request, Response any] interface {
	Send(Request) error
	CloseAndRecv() (Response, error)
	grpc.ClientStream
}

func relayClientStream[
	Request, Response any,
	Server inboundClientStream[Request, Response],
	OAPStream outboundClientStream[Request, Response],
	ARMSStream outboundClientStream[Request, Response],
](
	p *Proxy,
	method string,
	server Server,
	openOAP func(context.Context, ...grpc.CallOption) (OAPStream, error),
	openARMS func(context.Context, ...grpc.CallOption) (ARMSStream, error),
	mirror bool,
) (returnErr error) {
	started := time.Now()
	p.metrics.IncInflight("oap")
	defer func() {
		p.metrics.DecInflight("oap")
		p.observeOAP(method, started, returnErr)
	}()

	oapStream, err := openOAP(oapContext(server.Context()))
	if err != nil {
		return err
	}

	var side *streamMirror[Request]
	if mirror {
		side = startStreamMirror(p, method, openARMS)
	}
	for {
		message, recvErr := server.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			if side != nil {
				side.cancelFailed()
			}
			return recvErr
		}
		if sendErr := oapStream.Send(message); sendErr != nil {
			if side != nil {
				side.cancelFailed()
			}
			return sendErr
		}
		if side != nil && !side.enqueue(message) {
			side = nil
		}
	}
	if side != nil {
		side.finish(p.cfg.ARMSFinishTimeout)
	}

	response, closeErr := oapStream.CloseAndRecv()
	copyStreamMetadata(server, oapStream)
	if closeErr != nil {
		if side != nil {
			side.cancelFailed()
		}
		return closeErr
	}
	return server.SendAndClose(response)
}

type streamMirror[Request any] struct {
	queue     chan Request
	done      chan struct{}
	cancel    context.CancelFunc
	terminal  *armsTerminal
	closeOnce sync.Once
	closed    bool
}

type armsTerminal struct {
	once   sync.Once
	p      *Proxy
	method string
}

func (t *armsTerminal) finish(result string) {
	t.once.Do(func() { t.p.metrics.ObserveARMS(t.method, result) })
}

func startStreamMirror[
	Request, Response any,
	ARMSStream outboundClientStream[Request, Response],
](p *Proxy, method string, open func(context.Context, ...grpc.CallOption) (ARMSStream, error)) *streamMirror[Request] {
	release, ok := p.tryStartARMS(method)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithCancel(p.rootCtx)
	ctx = p.armsContext(ctx)
	side := &streamMirror[Request]{
		queue:    make(chan Request, p.cfg.ARMSStreamQueueSize),
		done:     make(chan struct{}),
		cancel:   cancel,
		terminal: &armsTerminal{p: p, method: method},
	}
	go func() {
		defer release()
		defer close(side.done)
		stream, err := open(ctx)
		if err != nil {
			side.terminal.finish("failed")
			p.logger.Warn("ARMS mirror failed", zap.String("method", method), zap.String("code", status.Code(err).String()))
			return
		}
		for message := range side.queue {
			if err := stream.Send(message); err != nil {
				side.terminal.finish("failed")
				p.logger.Warn("ARMS mirror failed", zap.String("method", method), zap.String("code", status.Code(err).String()))
				return
			}
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			side.terminal.finish("failed")
			p.logger.Warn("ARMS mirror failed", zap.String("method", method), zap.String("code", status.Code(err).String()))
			return
		}
		side.terminal.finish("succeeded")
	}()
	return side
}

func (s *streamMirror[Request]) enqueue(message Request) bool {
	if s.closed {
		return false
	}
	select {
	case <-s.done:
		s.closed = true
		return false
	case s.queue <- message:
		return true
	default:
		s.terminal.finish("dropped")
		s.cancel()
		s.closeQueue()
		return false
	}
}

func (s *streamMirror[Request]) finish(timeout time.Duration) {
	if s.closed {
		return
	}
	s.closeQueue()
	timer := time.AfterFunc(timeout, s.cancel)
	go func() {
		<-s.done
		timer.Stop()
	}()
}

func (s *streamMirror[Request]) cancelFailed() {
	if s == nil {
		return
	}
	s.cancel()
	s.closeQueue()
}

func (s *streamMirror[Request]) closeQueue() {
	s.closeOnce.Do(func() {
		s.closed = true
		close(s.queue)
	})
}

func copyStreamMetadata(server grpc.ServerStream, client grpc.ClientStream) {
	if header, err := client.Header(); err == nil && len(header) > 0 {
		_ = server.SetHeader(header)
	}
	if trailer := client.Trailer(); len(trailer) > 0 {
		server.SetTrailer(trailer)
	}
}
