//go:build !windows

package scripts

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPowerShellInstallBashShimNormalizesCRLFAndForwardsArguments(t *testing.T) {
	pwsh := portablePowerShell(t)
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("POSIX shell is unavailable: %v", err)
	}

	root := t.TempDir()
	binDir := filepath.Join(root, "bin with space")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", binDir, err)
	}

	setup := readSetupFixture(t, "setup.ps1")
	setup = strings.ReplaceAll(setup, "\r\n", "\n")
	setup = strings.ReplaceAll(setup, "\n", "\r\n")
	setupPath := filepath.Join(root, "setup-crlf.ps1")
	if err := os.WriteFile(setupPath, []byte(setup), 0o600); err != nil {
		t.Fatalf("WriteFile(CRLF setup.ps1) error = %v", err)
	}

	harness := fmt.Sprintf(`
$Source = [System.IO.File]::ReadAllText('%s')
$Tokens = $null
$ParseErrors = $null
$Ast = [System.Management.Automation.Language.Parser]::ParseInput($Source, [ref]$Tokens, [ref]$ParseErrors)
if ($ParseErrors.Count -ne 0) {
    throw "CRLF setup.ps1 parse failed: $($ParseErrors[0].Message)"
}
foreach ($FunctionName in @('Write-Info', 'Install-BashShim')) {
    $Function = $Ast.Find({
        param($Node)
        $Node -is [System.Management.Automation.Language.FunctionDefinitionAst] -and $Node.Name -eq $FunctionName
    }, $true)
    if ($null -eq $Function) {
        throw "Missing production function: $FunctionName"
    }
    Invoke-Expression $Function.Extent.Text
}
$BinDir = '%s'
Install-BashShim
`, psSingleQuoted(setupPath), psSingleQuoted(binDir))
	harnessPath := filepath.Join(root, "install-bash-shim.ps1")
	if err := os.WriteFile(harnessPath, []byte(harness), 0o600); err != nil {
		t.Fatalf("WriteFile(harness) error = %v", err)
	}

	command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", harnessPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("production Install-BashShim error = %v\n%s", err, output)
	}

	shimPath := filepath.Join(binDir, "slack-mgmt")
	shim, err := os.ReadFile(shimPath)
	if err != nil {
		t.Fatalf("ReadFile(generated shim) error = %v", err)
	}
	if bytes.HasPrefix(shim, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("production Install-BashShim generated a UTF-8 BOM")
	}
	if bytes.Contains(shim, []byte{'\r'}) {
		t.Fatalf("production Install-BashShim generated CR bytes: %q", shim)
	}

	executablePath := filepath.Join(binDir, "slack-mgmt.exe")
	executable := []byte("#!/bin/sh\nprintf '%s\\0' \"$@\"\n")
	if err := os.WriteFile(executablePath, executable, 0o700); err != nil {
		t.Fatalf("WriteFile(adjacent executable) error = %v", err)
	}

	args := []string{
		"argument with spaces",
		"dollar$semicolon;asterisk*question?",
		"single'quote and double\"quote",
		"backtick`pipe|ampersand&angles<>",
	}
	output, err := exec.Command(sh, append([]string{shimPath}, args...)...).Output()
	if err != nil {
		t.Fatalf("generated shim execution error = %v", err)
	}
	want := []byte(strings.Join(args, "\x00") + "\x00")
	if !bytes.Equal(output, want) {
		t.Fatalf("generated shim forwarded %q, want %q", output, want)
	}
}

func TestPowerShellVerifyInstallRejectsFailingNativeExecutable(t *testing.T) {
	pwsh := portablePowerShell(t)

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
	if err := os.WriteFile(filepath.Join(binDir, "slack-mgmt.exe"), []byte("#!/bin/sh\nexit 23\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(failing executable) error = %v", err)
	}

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

	command := exec.Command(pwsh, "-NoLogo", "-NoProfile", "-NonInteractive", "-File", harnessPath)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("Verify-Install accepted failing native executable:\n%s", output)
	}
	if !strings.Contains(string(output), "version smoke failed with exit code 23") {
		t.Fatalf("Verify-Install failure = %v, output:\n%s", err, output)
	}
}

func portablePowerShell(t *testing.T) string {
	t.Helper()
	pwsh := os.Getenv("SLACK_MGMT_TEST_PWSH")
	if pwsh != "" {
		return pwsh
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("PowerShell runtime is unavailable")
	}
	return pwsh
}
