package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Call sends a request and waits for a response.
func Call(ctx context.Context, pipeName string, req Request) (Response, error) {
	conn, err := dialPipe(ctx, pipeName)
	if err != nil {
		return Response{}, fmt.Errorf("dial pipe %s: %w", pipeName, err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	if req.ID == "" {
		req.ID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	if err := enc.Encode(req); err != nil {
		return Response{}, fmt.Errorf("encode request: %w", err)
	}

	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	return resp, nil
}

// Ping checks if Agent IPC is responsive.
func Ping(ctx context.Context, pipeName string) (bool, error) {
	resp, err := Call(ctx, pipeName, Request{Command: CmdPing})
	if err != nil {
		return false, err
	}
	return resp.Success, nil
}

// WaitForAgent retries ping until Agent is reachable or context expires.
func WaitForAgent(ctx context.Context, pipeName string, retryEvery time.Duration) error {
	if retryEvery <= 0 {
		retryEvery = 120 * time.Millisecond
	}

	ticker := time.NewTicker(retryEvery)
	defer ticker.Stop()

	for {
		callCtx, cancel := context.WithTimeout(ctx, DefaultConnectTimeout)
		ok, err := Ping(callCtx, pipeName)
		cancel()

		if err == nil && ok {
			return nil
		}

		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("%s: timeout waiting for agent", CodeAgentOffline)
			}
			return fmt.Errorf("%s: %w", CodeAgentOffline, ctx.Err())
		case <-ticker.C:
		}
	}
}
