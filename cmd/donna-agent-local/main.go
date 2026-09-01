package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kishansagathiya/donna/donna-server-go/internal/localagent"
	"github.com/kishansagathiya/donna/donna-server-go/internal/log"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

const version = "0.1.0"

func main() {
	apiBase := os.Getenv("DONNA_API_BASE")
	token := os.Getenv("DONNA_ACCESS_TOKEN")
	socket := os.Getenv("DONNA_IPC_SOCKET")
	secret := os.Getenv("DONNA_IPC_SECRET")
	support := os.Getenv("DONNA_SUPPORT_DIR")
	publicID := os.Getenv("DONNA_PUBLIC_DEVICE_ID")
	deviceName := os.Getenv("DONNA_DEVICE_NAME")
	arch := os.Getenv("DONNA_DEVICE_ARCH")
	if arch == "" {
		arch = "arm64"
	}
	if deviceName == "" {
		deviceName = "Mac"
	}
	if support == "" {
		home, _ := os.UserHomeDir()
		support = filepath.Join(home, "Library", "Application Support", "Donna")
	}
	if err := os.MkdirAll(support, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "support dir: %v\n", err)
		os.Exit(1)
	}

	if apiBase == "" || token == "" {
		fmt.Fprintln(os.Stderr, "DONNA_API_BASE and DONNA_ACCESS_TOKEN are required")
		os.Exit(1)
	}
	if publicID == "" {
		publicID = "dev-device"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	api := localagent.NewAPIClient(apiBase, token, "")
	var device storage.DesktopDevice
	if err := api.Do(ctx, "POST", "/desktop/devices/register", map[string]any{
		"public_device_id": publicID,
		"name":             deviceName,
		"platform":         "macos",
		"architecture":     arch,
		"app_version":      version,
		"capabilities": map[string]any{
			"workspace": true,
			"shell":     true,
			"browser":   true,
			"network":   true,
		},
	}, &device); err != nil {
		log.Error("device register failed", map[string]any{"error": err.Error()})
		os.Exit(1)
	}
	api.DeviceID = device.ID

	browserURL := os.Getenv("DONNA_BROWSER_URL")
	if script := os.Getenv("DONNA_BROWSER_SCRIPT"); script != "" && browserURL == "" {
		proc, err := localagent.StartBrowser(ctx, support, script, os.Getenv("DONNA_BROWSER_HEADED") == "1")
		if err != nil {
			log.Warn("local browser failed to start", map[string]any{"error": err.Error()})
		} else {
			browserURL = proc.URL
			defer proc.Stop()
		}
	}

	worker := &localagent.Worker{
		API:        api,
		Store:      &localagent.CloudStore{API: api, WorkerID: "desktop:" + device.ID},
		SupportDir: support,
		Workspaces: map[string]string{},
		BrowserURL: browserURL,
	}

	if socket != "" && secret != "" {
		go func() {
			err := localagent.ListenIPC(socket, secret, func(msg localagent.IPCMessage) localagent.IPCMessage {
				switch msg.Type {
				case "token":
					payload, err := localagent.ParsePayload[localagent.TokenPayload](msg)
					if err == nil {
						api.SetToken(payload.AccessToken)
					}
				case "workspaces":
					payload, err := localagent.ParsePayload[localagent.WorkspacesPayload](msg)
					if err == nil {
						m := map[string]string{}
						var sync []storage.WorkspaceSync
						for _, ws := range payload.Workspaces {
							m[ws.ID] = ws.Path
							sync = append(sync, storage.WorkspaceSync{ID: ws.ID, Name: ws.Name, Capabilities: map[string]any{"fs": true, "shell": true}})
						}
						worker.SetWorkspaces(m)
						_ = api.Do(context.Background(), "PUT", "/desktop/devices/"+device.ID+"/workspaces", map[string]any{"workspaces": sync}, nil)
					}
				case "pause":
					worker.SetPaused(true)
				case "resume":
					worker.SetPaused(false)
					go func() { _ = worker.DrainOnce(context.Background()) }()
				case "status":
					paused, active := worker.Snapshot()
					raw, _ := json.Marshal(localagent.StatusPayload{
						Worker:   version,
						DeviceID: device.ID,
						Paused:   paused,
						Active:   active,
					})
					return localagent.IPCMessage{Type: "status", Payload: raw}
				}
				return localagent.IPCMessage{Type: "ok"}
			})
			if err != nil {
				log.Warn("ipc listen failed", map[string]any{"error": err.Error()})
			}
		}()
	}

	log.Print("donna-agent-local started", map[string]any{"deviceId": device.ID, "version": version})
	for {
		if ctx.Err() != nil {
			return
		}
		if err := worker.Loop(ctx); err != nil && ctx.Err() == nil {
			log.Warn("desktop worker loop ended", map[string]any{"error": err.Error()})
			time.Sleep(3 * time.Second)
			continue
		}
		return
	}
}
