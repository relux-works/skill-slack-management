//go:build windows

package scripts

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const failingNativeHelperEnv = "SLACK_MGMT_SETUP_FAIL_NATIVE"

func TestMain(m *testing.M) {
	if os.Getenv(failingNativeHelperEnv) == "1" {
		os.Exit(23)
	}
	os.Exit(m.Run())
}

func TestWindowsVerifyInstallRejectsFailingNativeExecutable(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin with space")
	agentsDest := filepath.Join(root, ".agents", "skills", "slack-management")
	claudeDest := filepath.Join(root, ".claude", "skills", "slack-management")
	codexDest := filepath.Join(root, ".codex", "skills", "slack-management")
	for _, dir := range []string{binDir, agentsDest, claudeDest, codexDest} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}
	for _, dir := range []string{agentsDest, claudeDest, codexDest} {
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: slack-management\n---\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(SKILL.md) error = %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentsDest, "LICENSE"), []byte("Apache License\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(LICENSE) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "slack-mgmt"), []byte("shim"), 0o600); err != nil {
		t.Fatalf("WriteFile(shim) error = %v", err)
	}

	helperPath := filepath.Join(binDir, "slack-mgmt.exe")
	copyFile(t, os.Args[0], helperPath)

	windows := readSetupFixture(t, "setup.ps1")
	harness := setupFunctionsPrefix(t, windows) + fmt.Sprintf(`
$BinDir = '%s'
$BinaryName = 'slack-mgmt.exe'
$AgentsDest = '%s'
$ClaudeDest = '%s'
$CodexDest = '%s'
Verify-Install
`, psSingleQuoted(binDir), psSingleQuoted(agentsDest), psSingleQuoted(claudeDest), psSingleQuoted(codexDest))
	harnessPath := filepath.Join(root, "verify-failing-native.ps1")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatalf("WriteFile(harness) error = %v", err)
	}

	command := exec.Command("powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", harnessPath)
	command.Env = append(os.Environ(), failingNativeHelperEnv+"=1")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("Verify-Install accepted failing native executable:\n%s", output)
	}
	if !strings.Contains(string(output), "version smoke failed with exit code 23") {
		t.Fatalf("Verify-Install failure = %v, output:\n%s", err, output)
	}
}

func copyFile(t *testing.T, source, destination string) {
	t.Helper()
	in, err := os.Open(source)
	if err != nil {
		t.Fatalf("Open(%q) error = %v", source, err)
	}
	defer in.Close()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatalf("OpenFile(%q) error = %v", destination, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatalf("Copy(%q) error = %v", destination, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("Close(%q) error = %v", destination, err)
	}
}
