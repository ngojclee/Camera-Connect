//go:build windows

package ipc

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestServerClient_PingAndGetStatus(t *testing.T) {
	pipeName := fmt.Sprintf(`\\.\pipe\CameraConnectIPC_test_%d`, time.Now().UnixNano())

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	server := NewServer(pipeName, 2*time.Second, func(ctx context.Context, req Request) Response {
		switch req.Command {
		case CmdPing:
			return Response{Success: true, Code: CodeOK, Data: "pong"}
		case CmdGetStatus:
			return Response{
				Success: true,
				Code:    CodeOK,
				Data: AppStatus{
					TrayColor:  "green",
					StatusText: "Sẵn sàng",
					SyncInProgress: false,
				},
			}
		default:
			return Response{
				Success: false,
				Code:    CodeUnknownCmd,
				Error:   "unsupported command",
			}
		}
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(serverCtx)
	}()

	// Retry ping for a short period while server starts listening.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPing()

	ready := false
	for pingCtx.Err() == nil {
		callCtx, cancelCall := context.WithTimeout(context.Background(), 300*time.Millisecond)
		ok, err := Ping(callCtx, pipeName)
		cancelCall()
		if err == nil && ok {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatal("ipc server did not become ready for ping in time")
	}

	callCtx, cancelCall := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelCall()

	resp, err := Call(callCtx, pipeName, Request{
		Command: CmdGetStatus,
	})
	if err != nil {
		t.Fatalf("get_status call failed: %v", err)
	}
	if !resp.Success {
		t.Fatalf("get_status returned success=false, code=%s error=%s", resp.Code, resp.Error)
	}

	cancelServer()
	_ = server.Close()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server exited with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not exit after cancellation")
	}
}

func TestWaitForAgent_RetriesUntilOnline(t *testing.T) {
	pipeName := fmt.Sprintf(`\\.\pipe\CameraConnectIPC_test_wait_%d`, time.Now().UnixNano())

	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	server := NewServer(pipeName, 2*time.Second, func(ctx context.Context, req Request) Response {
		if req.Command == CmdPing {
			return Response{Success: true, Code: CodeOK}
		}
		return Response{Success: false, Code: CodeUnknownCmd}
	})

	// Start server with delay to validate retry behavior.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = server.Start(serverCtx)
	}()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelWait()

	if err := WaitForAgent(waitCtx, pipeName, 80*time.Millisecond); err != nil {
		t.Fatalf("WaitForAgent should succeed after delayed server start: %v", err)
	}

	cancelServer()
	_ = server.Close()
}
