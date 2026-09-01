package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/kishansagathiya/donna/donna-server-go/internal/config"
	"github.com/kishansagathiya/donna/donna-server-go/internal/featureflags"
	"github.com/kishansagathiya/donna/donna-server-go/internal/storage"
)

func TestRouteExecutionCloudWhenFlagOff(t *testing.T) {
	s := &Spawner{
		Flags: &featureflags.Resolver{Defaults: &config.Config{LocalAgentsV1: false}},
	}
	route, err := s.routeExecution(context.Background(), "user", SpawnInput{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if route.Local || route.Target != storage.ExecutionTargetCloud {
		t.Fatalf("expected cloud route, got %+v", route)
	}
}

func TestRouteExecutionDesktopRequired(t *testing.T) {
	s := &Spawner{
		Flags: &featureflags.Resolver{Defaults: &config.Config{LocalAgentsV1: true}},
	}
	_, err := s.routeExecution(context.Background(), "user", SpawnInput{}, nil)
	if err == nil || !strings.Contains(err.Error(), "desktop_required") {
		t.Fatalf("expected desktop_required, got %v", err)
	}
}

func TestDefaultLocalToolAllowlist(t *testing.T) {
	without := DefaultLocalToolAllowlist(false)
	for _, name := range without {
		if name == "workspace" || name == "process" {
			t.Fatal("workspace/process must require a workspace")
		}
	}
	with := DefaultLocalToolAllowlist(true)
	foundWS, foundProc := false, false
	for _, name := range with {
		if name == "workspace" {
			foundWS = true
		}
		if name == "process" {
			foundProc = true
		}
	}
	if !foundWS || !foundProc {
		t.Fatalf("expected workspace+process, got %v", with)
	}
}

func TestSkipCloudClaimForLocalRuns(t *testing.T) {
	run := storage.AgentRun{ExecutionTarget: storage.ExecutionTargetLocal, Status: storage.AgentStatusQueued}
	if run.ExecutionTarget != storage.ExecutionTargetLocal {
		t.Fatal("local runs must never be claimed by the cloud worker")
	}
}
