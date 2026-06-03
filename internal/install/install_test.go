package install

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallBuildsBinaryAndWritesLauncher(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	var built BuildRequest
	result, err := Install(context.Background(), Options{
		Home:     home,
		RepoRoot: repo,
		PathEnv:  filepath.Join(home, ".local", "bin"),
		Build: func(_ context.Context, request BuildRequest) error {
			built = request
			return os.WriteFile(request.OutputPath, []byte("binary"), 0o755)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	expectedBinary := filepath.Join(home, ".mini-code", "bin", "minicode-go")
	expectedLauncher := filepath.Join(home, ".local", "bin", "minicode")
	if built.RepoRoot != repo || built.OutputPath != expectedBinary {
		t.Fatalf("unexpected build request: %#v", built)
	}
	if result.BinaryPath != expectedBinary || result.LauncherPath != expectedLauncher {
		t.Fatalf("unexpected install result: %#v", result)
	}
	launcher, err := os.ReadFile(expectedLauncher)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(launcher), `exec "`+expectedBinary+`" "$@"`) {
		t.Fatalf("unexpected launcher:\n%s", string(launcher))
	}
	info, err := os.Stat(expectedLauncher)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("launcher should be executable, got %v", info.Mode().Perm())
	}
	if result.NeedsPathHint {
		t.Fatalf("PATH hint should be false when bin dir is already present: %#v", result)
	}
}

func TestInstallReportsPathHintWhenLocalBinMissing(t *testing.T) {
	home := t.TempDir()
	result, err := Install(context.Background(), Options{
		Home:     home,
		RepoRoot: t.TempDir(),
		PathEnv:  "/usr/bin:/bin",
		Build: func(_ context.Context, request BuildRequest) error {
			return os.WriteFile(request.OutputPath, []byte("binary"), 0o755)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsPathHint || result.PathExport == "" || !strings.Contains(result.PathExport, filepath.Join(home, ".local", "bin")) {
		t.Fatalf("expected PATH hint, got %#v", result)
	}
}
