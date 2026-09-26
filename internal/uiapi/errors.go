package uiapi

import (
	"time"

	"github.com/ngojclee/camera-connect/internal/ipc"
)

func nowRFC3339(nowFn func() time.Time) string {
	return nowFn().Format(time.RFC3339)
}

func badRequestEnvelope(nowFn func() time.Time, message string) ActionEnvelope {
	return ActionEnvelope{
		OK:      false,
		Success: false,
		Code:    ipc.CodeBadRequest,
		Error:   message,
		Server:  nowRFC3339(nowFn),
	}
}

func agentOfflineEnvelope(nowFn func() time.Time, err error) ActionEnvelope {
	return ActionEnvelope{
		OK:      false,
		Success: false,
		Code:    ipc.CodeAgentOffline,
		Error:   err.Error(),
		Server:  nowRFC3339(nowFn),
	}
}

func responseEnvelope(nowFn func() time.Time, resp ipc.Response) ActionEnvelope {
	return ActionEnvelope{
		OK:      true,
		ID:      resp.ID,
		Success: resp.Success,
		Code:    resp.Code,
		Error:   resp.Error,
		Data:    resp.Data,
		Server:  nowRFC3339(nowFn),
	}
}
