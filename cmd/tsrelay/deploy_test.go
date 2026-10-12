package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moduleRoot finds the repository root the same way TestNoFunnel does.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	return root
}

func readDeployFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(moduleRoot(t), rel))
	if err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return string(b)
}

func TestDockerfile(t *testing.T) {
	s := readDeployFile(t, "Dockerfile")
	for _, want := range []string{
		"golang:1.27",
		"CGO_ENABLED=0",
		"distroless/static-debian12:nonroot",
		"TSRELAY_STATE_DIR=/var/lib/tsrelay",
		`ENTRYPOINT ["/tsrelay", "serve"]`,
		// The Go build runs natively and cross-compiles for the target platform.
		"FROM --platform=$BUILDPLATFORM golang:",
		"ARG TARGETOS TARGETARCH",
		"GOOS=$TARGETOS GOARCH=$TARGETARCH",
		// The nonroot user (65532) must own the state directory.
		"RUN mkdir -p /out/var/lib/tsrelay",
		"COPY --from=builder --chown=65532:65532 /out/var/lib/tsrelay /var/lib/tsrelay",
		// A numeric user, so that the kubelet can verify runAsNonRoot.
		"USER 65532:65532",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	// Changes to a volume path after VOLUME are discarded, so the owned
	// directory must be copied in before it.
	if c, v := strings.Index(s, "--chown=65532:65532"), strings.Index(s, "VOLUME"); c < 0 || v < 0 || c > v {
		t.Error("Dockerfile must copy the owned state directory before VOLUME")
	}
}

func TestDockerignoreExists(t *testing.T) {
	s := readDeployFile(t, ".dockerignore")
	if !strings.Contains(s, ".git") {
		t.Error(".dockerignore should exclude .git")
	}
}

func TestKubernetesStatefulSet(t *testing.T) {
	s := readDeployFile(t, "deploy/kubernetes/statefulset.yaml")
	for _, want := range []string{
		"kind: StatefulSet",
		"replicas: 1",
		"/var/lib/tsrelay",
		"TS_CLIENT_SECRET",
		"--ops-listen=:9090",
		"path: /healthz",
		"path: /readyz",
		"serviceAccountName: tsrelay",
		// The volume must be writable by the image's nonroot user.
		"fsGroup: 65532",
		"runAsNonRoot: true",
		"runAsUser: 65532",
		"runAsGroup: 65532",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("statefulset.yaml missing %q", want)
		}
	}
}

// No Service is shipped: the relay is reached over the tailnet only (DESIGN.md §14).
func TestNoKubernetesService(t *testing.T) {
	dir := filepath.Join(moduleRoot(t), "deploy", "kubernetes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("deploy/kubernetes is empty")
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "kind: Service\n") {
			t.Errorf("%s declares a Service; the relay must be reached over the tailnet only", e.Name())
		}
	}
}

func TestComposeFile(t *testing.T) {
	s := readDeployFile(t, "deploy/compose.yaml")
	for _, want := range []string{"/var/lib/tsrelay", "TS_CLIENT_SECRET", "volumes:"} {
		if !strings.Contains(s, want) {
			t.Errorf("compose.yaml missing %q", want)
		}
	}
}

func TestExamplePolicy(t *testing.T) {
	s := readDeployFile(t, "deploy/policy.example.hujson")
	for _, want := range []string{
		"tag:tsrelay",
		"mazetti.me/cap/relay",
		`"roles": ["receiver"]`,
		`"roles": ["sender"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("policy.example.hujson missing %q", want)
		}
	}
}
