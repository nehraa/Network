//go:build docker_integration
// +build docker_integration

package mixnet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDockerFailureAndRecoverFromFailure(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	script := filepath.Join(mixnetRootDir(t), "tests", "docker", "run-docker-tests.sh")
	cmd := exec.CommandContext(ctx, "bash", script)
	cmd.Dir = repoRootDir(t)
	cmd.Env = append(os.Environ(),
		"TARGET_TEST=TestProductionSanity/mixnet_api_end_to_end/failure_and_recover_from_failure",
		"MIXNET_SANITY_VERBOSE_LOGS=0",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker failure-recovery integration failed: %v\n%s", err, string(output))
	}
}

func TestDockerComposeUpDown(t *testing.T) {
	requireDocker(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	composeFile := dockerComposeTestFile(t)
	t.Cleanup(func() {
		_, _ = runDockerCommand(ctx, t, "compose", "-f", composeFile, "down")
	})

	if output, err := runDockerCommand(ctx, t, "compose", "-f", composeFile, "up", "-d", "--build"); err != nil {
		t.Fatalf("docker compose up failed: %v\n%s", err, string(output))
	}

	time.Sleep(5 * time.Second)

	output, err := runDockerCommand(ctx, t, "compose", "-f", composeFile, "ps", "--services", "--status", "running")
	if err != nil {
		t.Fatalf("docker compose ps failed: %v\n%s", err, string(output))
	}
	running := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line == "" {
			continue
		}
		running[line] = struct{}{}
	}

	for _, service := range []string{
		"mixnet-origin",
		"mixnet-destination",
		"mixnet-relay-1",
		"mixnet-relay-2",
		"mixnet-relay-3",
		"mixnet-relay-4",
		"mixnet-relay-5",
		"mixnet-relay-6",
		"mixnet-relay-7",
	} {
		if _, ok := running[service]; !ok {
			t.Fatalf("service %s is not running; running=%v", service, mapsKeys(running))
		}
	}

	if output, err := runDockerCommand(ctx, t, "compose", "-f", composeFile, "down"); err != nil {
		t.Fatalf("docker compose down failed: %v\n%s", err, string(output))
	}
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := runDockerCommand(context.Background(), t, "ps"); err != nil {
		t.Skip("Docker is not available")
	}
	if _, err := runDockerCommand(context.Background(), t, "compose", "version"); err != nil {
		t.Skip("Docker Compose is not available")
	}
}

func runDockerCommand(ctx context.Context, t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = repoRootDir(t)
	return cmd.CombinedOutput()
}

func repoRootDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func mixnetRootDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRootDir(t), "mixnet")
}

func dockerComposeTestFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(mixnetRootDir(t), "tests", "docker", "docker-compose.test.yml")
}

func mapsKeys(m map[string]struct{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	return keys
}
