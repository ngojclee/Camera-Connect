package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/ngojclee/camera-connect/internal/ipc"
	"github.com/ngojclee/camera-connect/internal/uiapi"
)

var Version = "dev"

type actionEnvelope = uiapi.ActionEnvelope

func main() {
	action := flag.String("action", "", "Run one IPC action and print JSON result")
	payload := flag.String("payload", "", "Optional JSON payload")
	pipeName := flag.String("pipe", ipc.PipeName, "Named pipe path for Agent IPC")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(Version)
		return
	}

	log.Printf("[INFO] CameraConnect UI %s", Version)

	if *action != "" {
		env := runAction(*action, *payload, *pipeName)
		printJSON(env)
		if env.OK {
			return
		}
		os.Exit(1)
	}

	guard, acquired, err := acquireUISingleInstance()
	if err != nil {
		log.Fatalf("Failed to acquire UI single-instance guard: %v", err)
	}
	if !acquired {
		log.Println("[INFO] Another UI instance is running. Focusing it...")
		// Assuming we always run wails now:
		if focusErr := focusExistingUIWindow("wails"); focusErr != nil {
			log.Printf("[WARN] Failed to focus existing UI window: %v", focusErr)
		}
		return
	}
	defer guard.Release()

	// Wait briefly for agent, but don't fail if we can't reach it. The UI can show a "disconnected" state.
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancelWait()
	if err := ipc.WaitForAgent(waitCtx, *pipeName, 150*time.Millisecond); err != nil {
		log.Printf("[WARN] Agent not reachable at startup: %v", err)
	} else {
		log.Println("[INFO] Agent reachable.")
	}

	if err := launchWailsRuntime(*pipeName); err != nil {
		log.Printf("[ERROR] Failed to launch Wails runtime: %v", err)
		os.Exit(1)
	}
}

func runAction(action, payload, pipeName string) actionEnvelope {
	service := uiapi.NewService(pipeName)
	return service.ExecuteAction(action, payload)
}

func printJSON(payload any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(payload)
}
