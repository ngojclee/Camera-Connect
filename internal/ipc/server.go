package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// RequestHandler handles a single request and returns a response.
type RequestHandler func(context.Context, Request) Response

// Server implements a simple JSON request-response named-pipe server.
// Protocol: one request, one response per connection.
type Server struct {
	pipeName       string
	requestTimeout time.Duration
	handler        RequestHandler

	mu       sync.Mutex
	listener net.Listener
}

func NewServer(pipeName string, requestTimeout time.Duration, handler RequestHandler) *Server {
	if requestTimeout <= 0 {
		requestTimeout = DefaultRequestTimeout
	}
	return &Server{
		pipeName:       pipeName,
		requestTimeout: requestTimeout,
		handler:        handler,
	}
}

func (s *Server) Start(ctx context.Context) error {
	ln, err := listenPipe(s.pipeName)
	if err != nil {
		return fmt.Errorf("listen pipe %s: %w", s.pipeName, err)
	}

	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	go func() {
		<-ctx.Done()
		_ = s.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Printf("[WARN] IPC accept failed: %v", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return nil
	}
	err := s.listener.Close()
	s.listener = nil
	return err
}

func (s *Server) handleConn(parent context.Context, conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	var req Request
	if err := dec.Decode(&req); err != nil {
		_ = enc.Encode(Response{
			Success: false,
			Error:   "invalid request payload",
			Code:    CodeBadRequest,
		})
		return
	}

	if req.ID == "" {
		req.ID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	if s.handler == nil {
		_ = enc.Encode(Response{
			ID:      req.ID,
			Success: false,
			Error:   "request handler is not configured",
			Code:    CodeInternalError,
		})
		return
	}

	reqCtx, cancel := context.WithTimeout(parent, s.requestTimeout)
	defer cancel()

	resp := s.handler(reqCtx, req)
	if resp.ID == "" {
		resp.ID = req.ID
	}
	if resp.Code == "" {
		if resp.Success {
			resp.Code = CodeOK
		} else {
			resp.Code = CodeInternalError
		}
	}

	_ = enc.Encode(resp)
}
