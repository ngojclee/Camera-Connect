package uiapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ngojclee/camera-connect/internal/ipc"
)

func fixedNow() time.Time {
	return time.Date(2026, 3, 30, 9, 0, 0, 0, time.UTC)
}

func newTestService() *Service {
	s := NewService(ipc.PipeName)
	s.now = fixedNow
	s.ctxFor = func(time.Duration) (context.Context, context.CancelFunc) {
		return context.WithCancel(context.Background())
	}
	return s
}

func TestExecuteAction_UnknownCommand(t *testing.T) {
	s := newTestService()
	got := s.ExecuteAction("unknown", "")
	if got.OK {
		t.Fatalf("OK = true, want false")
	}
	if got.Code != ipc.CodeBadRequest {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeBadRequest)
	}
}

func TestSaveConfig_InvalidPayload(t *testing.T) {
	s := newTestService()
	got := s.SaveConfig("{bad")
	if got.OK {
		t.Fatalf("OK = true, want false")
	}
	if got.Code != ipc.CodeBadRequest {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeBadRequest)
	}
}

func TestScanNow_InvalidPayload(t *testing.T) {
	s := newTestService()
	got := s.ScanNow("{bad")
	if got.OK {
		t.Fatalf("OK = true, want false")
	}
	if got.Code != ipc.CodeBadRequest {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeBadRequest)
	}
}

func TestSetProfile_EmptyID(t *testing.T) {
	s := newTestService()
	got := s.SetProfile("   ")
	if got.OK {
		t.Fatalf("OK = true, want false")
	}
	if got.Code != ipc.CodeBadRequest {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeBadRequest)
	}
}

func TestGetStatus_AgentOffline(t *testing.T) {
	s := newTestService()
	s.call = func(context.Context, string, ipc.Request) (ipc.Response, error) {
		return ipc.Response{}, errors.New("pipe unavailable")
	}

	got := s.GetStatus()
	if got.OK {
		t.Fatalf("OK = true, want false")
	}
	if got.Code != ipc.CodeAgentOffline {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeAgentOffline)
	}
}

func TestGetStatus_ResponseEnvelope(t *testing.T) {
	s := newTestService()
	s.call = func(context.Context, string, ipc.Request) (ipc.Response, error) {
		return ipc.Response{
			ID:      "req-1",
			Success: true,
			Code:    ipc.CodeOK,
			Data: map[string]any{
				"status_text": "Sẵn sàng",
			},
		}, nil
	}

	got := s.GetStatus()
	if !got.OK {
		t.Fatalf("OK = false, want true")
	}
	if got.ID != "req-1" {
		t.Fatalf("ID = %q, want req-1", got.ID)
	}
	if got.Code != ipc.CodeOK {
		t.Fatalf("Code = %q, want %q", got.Code, ipc.CodeOK)
	}
}
