package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestSetupScriptsExposeEquivalentInstallAndVerifyContracts(t *testing.T) {
	repoRoot := filepath.Clean("..")
	unix := readSetupFixture(t, filepath.Join(repoRoot, "scripts", "setup.sh"))
	windows := readSetupFixture(t, filepath.Join(repoRoot, "scripts", "setup.ps1"))

	contracts := []struct {
		name         string
		unixFragment string
		winFragment  string
	}{
		{name: "build installed CLI", unixFragment: "go build -trimpath", winFragment: "go build -trimpath"},
		{name: "install shared skill", unixFragment: "install_skill_artifact", winFragment: "Install-SkillArtifact"},
		{name: "install optional integrations", unixFragment: "install_optional_integrations", winFragment: "Install-OptionalIntegrations"},
		{name: "refresh provider links", unixFragment: "refresh_links", winFragment: "Refresh-Links"},
		{name: "write install state", unixFragment: "write_install_state", winFragment: "Write-InstallState"},
		{name: "version smoke", unixFragment: `"$dest" version`, winFragment: `-Description "version smoke"`},
		{name: "query schema smoke", unixFragment: `"$dest" q 'schema()'`, winFragment: `-Description "query schema smoke"`},
		{name: "provider capabilities smoke", unixFragment: `"$dest" q 'provider_capabilities()'`, winFragment: `-Description "provider capabilities smoke"`},
		{name: "mutation schema smoke", unixFragment: `"$dest" m 'schema()'`, winFragment: `-Description "mutation schema smoke"`},
		{name: "auth path smoke", unixFragment: `"$dest" auth config-path`, winFragment: `-Description "auth config-path smoke"`},
	}

	for _, contract := range contracts {
		t.Run(contract.name, func(t *testing.T) {
			if !strings.Contains(unix, contract.unixFragment) {
				t.Errorf("scripts/setup.sh lacks contract fragment %q", contract.unixFragment)
			}
			if !strings.Contains(windows, contract.winFragment) {
				t.Errorf("scripts/setup.ps1 lacks contract fragment %q", contract.winFragment)
			}
		})
	}

	for _, forbidden := range []string{"auth resolve", "auth whoami", "slack.com/api"} {
		if strings.Contains(unix, forbidden) || strings.Contains(windows, forbidden) {
			t.Errorf("setup smoke must remain credential-free; found %q", forbidden)
		}
	}
}

func TestSetupSkillArtifactExcludesScratchAndPreservesCommandSource(t *testing.T) {
	unix := readSetupFixture(t, "setup.sh")
	windows := readSetupFixture(t, "setup.ps1")
	for _, fragment := range []string{`--exclude='/AGENTS.md'`, `--exclude='/.temp'`, `--exclude='/LOGBOOK.md'`, `--exclude='/scripts/verify-agent-safety-contract.sh'`, `--exclude='/slack-mgmt'`, `--exclude='/slack-mgmt.exe'`} {
		if !strings.Contains(unix, fragment) {
			t.Errorf("scripts/setup.sh lacks artifact exclusion %q", fragment)
		}
	}
	if !strings.Contains(unix, "rsync -a --delete --delete-excluded") {
		t.Fatal("scripts/setup.sh does not purge artifacts excluded by a newer installer")
	}
	if strings.Contains(unix, `--exclude='slack-mgmt'`) || strings.Contains(unix, `--exclude='slack-mgmt.exe'`) {
		t.Fatal("scripts/setup.sh uses an unanchored binary exclusion that removes cmd/slack-mgmt")
	}
	if !strings.Contains(windows, `".git", ".gitignore", ".gitattributes", ".gitmodules", "AGENTS.md", "CLAUDE.md", "LOGBOOK.md", ".temp", ".task-board"`) {
		t.Fatal("scripts/setup.ps1 does not exclude generated runtime or scratch artifacts")
	}

	rsync, err := exec.LookPath("rsync")
	if err != nil {
		if runtime.GOOS == "windows" {
			return
		}
		t.Fatalf("rsync is required by scripts/setup.sh: %v", err)
	}
	source := filepath.Join(t.TempDir(), "source")
	destination := filepath.Join(t.TempDir(), "installed")
	for path, content := range map[string]string{
		"SKILL.md":                 "---\nname: slack-management\n---\n",
		"AGENTS.md":                "generated host runtime\n",
		"slack-mgmt":               "root binary",
		"slack-mgmt.exe":           "root windows binary",
		".temp/private-review.log": "scratch",
		"LOGBOOK.md":               "local agent logbook",
		"scripts/verify-agent-safety-contract.sh": "local verifier",
		"nested/.git/config":                      "git debris",
		"nested/.gitignore":                       "git debris",
		"cmd/slack-mgmt/main.go":                  "package main\n",
		"LICENSE":                                 "Apache License\n",
	} {
		absolute := filepath.Join(source, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, stale := range []string{".temp/stale-review.log", "slack-mgmt"} {
		absolute := filepath.Join(destination, filepath.FromSlash(stale))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("stale installed artifact"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	matches := regexp.MustCompile(`--exclude='([^']+)'`).FindAllStringSubmatch(unix, -1)
	args := []string{"-a", "--delete", "--delete-excluded"}
	for _, match := range matches {
		args = append(args, "--exclude="+match[1])
	}
	args = append(args, source+string(os.PathSeparator), destination+string(os.PathSeparator))
	if output, err := exec.Command(rsync, args...).CombinedOutput(); err != nil {
		t.Fatalf("rsync artifact fixture error = %v\n%s", err, output)
	}
	for _, missing := range []string{"AGENTS.md", "LOGBOOK.md", "scripts/verify-agent-safety-contract.sh", "slack-mgmt", "slack-mgmt.exe", ".temp", "nested/.git", "nested/.gitignore"} {
		if _, err := os.Stat(filepath.Join(destination, missing)); !os.IsNotExist(err) {
			t.Fatalf("excluded artifact %q exists or returned unexpected error: %v", missing, err)
		}
	}
	if _, err := os.Stat(filepath.Join(destination, "cmd", "slack-mgmt", "main.go")); err != nil {
		t.Fatalf("cmd/slack-mgmt source was excluded: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "LICENSE")); err != nil {
		t.Fatalf("LICENSE was excluded: %v", err)
	}
}

func TestSetupScriptsExecuteExactTopLevelPhaseGraphs(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		phases []string
	}{
		{
			name: "macOS",
			path: "setup.sh",
			phases: []string{
				"install_go",
				"compute_ldflags",
				"build_cli",
				"install_binary",
				"install_skill_artifact",
				"install_optional_integrations",
				"refresh_links",
				"write_install_state",
				"verify_install",
			},
		},
		{
			name: "Windows",
			path: "setup.ps1",
			phases: []string{
				"Ensure-Go",
				"Get-VersionMetadata",
				"Build-Cli",
				"Install-Binary",
				"Install-BashShim",
				"Install-SkillArtifact",
				"Install-OptionalIntegrations",
				"Refresh-Links",
				"Write-InstallState",
				"Ensure-UserPath",
				"Verify-Install",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := readSetupFixture(t, tt.path)
			got := topLevelPhaseCalls(t, script, tt.phases)
			if !reflect.DeepEqual(got, tt.phases) {
				t.Fatalf("%s top-level phase graph = %#v, want %#v", tt.path, got, tt.phases)
			}
		})
	}
}

func TestWindowsInstalledSmokesCheckEveryNativeExit(t *testing.T) {
	windows := readSetupFixture(t, "setup.ps1")
	required := []string{
		"function Invoke-NativeChecked",
		"& $FilePath @ArgumentList | Out-Null",
		"$ExitCode = $LASTEXITCODE",
		"if ($ExitCode -ne 0)",
		`throw "$Description failed with exit code $ExitCode"`,
		`Invoke-NativeChecked -Description "version smoke" -FilePath $InstalledBinary -ArgumentList @("version")`,
		`Invoke-NativeChecked -Description "query schema smoke" -FilePath $InstalledBinary -ArgumentList @("q", "schema()", "--format", "json")`,
		`Invoke-NativeChecked -Description "provider capabilities smoke" -FilePath $InstalledBinary -ArgumentList @("q", "provider_capabilities()", "--format", "json")`,
		`Invoke-NativeChecked -Description "mutation schema smoke" -FilePath $InstalledBinary -ArgumentList @("m", "schema()", "--format", "json")`,
		`Invoke-NativeChecked -Description "auth config-path smoke" -FilePath $InstalledBinary -ArgumentList @("auth", "config-path")`,
		`Invoke-NativeChecked -Description "agents-infra version smoke"`,
	}
	for _, fragment := range required {
		if !strings.Contains(windows, fragment) {
			t.Errorf("scripts/setup.ps1 lacks fail-closed native smoke fragment %q", fragment)
		}
	}
	if got := strings.Count(windows, "Invoke-NativeChecked -Description"); got != 6 {
		t.Fatalf("scripts/setup.ps1 checked native smoke count = %d, want 6", got)
	}
	if strings.Contains(windows, "& $InstalledBinary") {
		t.Fatal("scripts/setup.ps1 bypasses checked native invocation")
	}
}

func TestWindowsSetupInstallsCallableShimAndUserPath(t *testing.T) {
	windows := readSetupFixture(t, filepath.Join("setup.ps1"))
	required := []string{
		"function Install-BashShim",
		`Join-Path $BinDir "slack-mgmt"`,
		`exec "` + "`" + `$DIR/slack-mgmt.exe" "` + "`" + `$@"`,
		"System.Text.UTF8Encoding($false)",
		"function Ensure-UserPath",
		`SetEnvironmentVariable("Path", $NewPath, "User")`,
		"Install-BashShim",
		"Ensure-UserPath",
		"Missing installed bash shim",
	}
	for _, fragment := range required {
		if !strings.Contains(windows, fragment) {
			t.Errorf("scripts/setup.ps1 lacks Windows setup gate %q", fragment)
		}
	}
}

func TestRootSetupWrappersDelegateAndPreserveArguments(t *testing.T) {
	unix := readSetupFixture(t, filepath.Join("..", "setup.sh"))
	windows := readSetupFixture(t, filepath.Join("..", "setup.ps1"))
	if !strings.Contains(unix, `exec zsh "$SCRIPT_DIR/scripts/setup.sh" "$@"`) {
		t.Fatalf("root setup.sh does not exec canonical installer with all arguments")
	}
	if !strings.Contains(windows, `& (Join-Path $ScriptDir "scripts\setup.ps1") @args`) || !strings.Contains(windows, "exit $LASTEXITCODE") {
		t.Fatalf("root setup.ps1 does not delegate with arguments and exit code")
	}
}

func TestRootUnixSetupWrapperExecutesCanonicalHelpAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix wrapper execution is not available on Windows")
	}
	rootWrapper := filepath.Join("..", "setup.sh")

	output, err := exec.Command(rootWrapper, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("setup.sh --help error = %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Usage: scripts/setup.sh [options]") || !strings.Contains(string(output), "--install-only") || !strings.Contains(string(output), "--with-attachments") {
		t.Fatalf("setup.sh --help did not reach canonical installer:\n%s", output)
	}

	output, err = exec.Command(rootWrapper, "--definitely-invalid").CombinedOutput()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("setup.sh invalid option error = %T %v, want exit 1\n%s", err, err, output)
	}
	if !strings.Contains(string(output), "Unknown option: --definitely-invalid") {
		t.Fatalf("setup.sh invalid option did not preserve argument:\n%s", output)
	}
}

func TestSetupOptionalIntegrationsAreExplicitAndFailClosed(t *testing.T) {
	unix := readSetupFixture(t, "setup.sh")
	windows := readSetupFixture(t, "setup.ps1")
	for _, fragment := range []string{"--with-attachments", "--with-browser", "--with-slackdump", "requires agents-infra", "requires mac-chrome-session", "requires an official Slackdump v4"} {
		if !strings.Contains(unix, fragment) {
			t.Errorf("scripts/setup.sh lacks optional integration contract %q", fragment)
		}
	}
	for _, fragment := range []string{"WithAttachments", "WithBrowser", "WithSlackdump", "requires agents-infra", "browser transport is macOS-only", "requires an official Slackdump v4"} {
		if !strings.Contains(windows, fragment) {
			t.Errorf("scripts/setup.ps1 lacks optional integration contract %q", fragment)
		}
	}
	if !strings.Contains(unix, `[[ -f "$AGENTS_DEST/LICENSE" ]]`) || !strings.Contains(windows, `Join-Path $AgentsDest "LICENSE"`) {
		t.Fatal("setup scripts do not verify packaged license metadata")
	}
}

func TestWindowsGeneratedBashShimForwardsEveryArgument(t *testing.T) {
	windows := readSetupFixture(t, filepath.Join("setup.ps1"))
	startMarker := "$ShimContent = @\"\n"
	endMarker := "\n\"@"
	start := strings.Index(windows, startMarker)
	if start < 0 {
		t.Fatal("scripts/setup.ps1 lacks bash shim here-string")
	}
	start += len(startMarker)
	end := strings.Index(windows[start:], endMarker)
	if end < 0 {
		t.Fatal("scripts/setup.ps1 bash shim here-string is unterminated")
	}
	shimContent := windows[start : start+end]
	shimContent = strings.ReplaceAll(shimContent, "`$", "$")

	binDir := filepath.Join(t.TempDir(), "bin with space")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	executable := filepath.Join(binDir, "slack-mgmt.exe")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '<%s>\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(fake exe) error = %v", err)
	}
	shim := filepath.Join(binDir, "slack-mgmt")
	if err := os.WriteFile(shim, []byte(shimContent), 0o700); err != nil {
		t.Fatalf("WriteFile(shim) error = %v", err)
	}

	args := []string{"first arg", "dollar$and;semi", "third"}
	output, err := exec.Command("sh", append([]string{shim}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("generated bash shim error = %v\n%s", err, output)
	}
	want := "<first arg>\n<dollar$and;semi>\n<third>\n"
	if string(output) != want {
		t.Fatalf("generated bash shim output = %q, want %q", output, want)
	}
}

func readSetupFixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return string(data)
}

func topLevelPhaseCalls(t *testing.T, script string, knownPhases []string) []string {
	t.Helper()
	lines, lastFunctionClose := setupFunctionLines(t, script)
	known := make(map[string]struct{}, len(knownPhases))
	for _, phase := range knownPhases {
		known[phase] = struct{}{}
	}

	var calls []string
	for _, line := range lines[lastFunctionClose+1:] {
		candidate := strings.TrimSpace(line)
		if _, ok := known[candidate]; ok {
			calls = append(calls, candidate)
		}
	}
	return calls
}

func setupFunctionsPrefix(t *testing.T, script string) string {
	t.Helper()
	lines, lastFunctionClose := setupFunctionLines(t, script)
	return strings.Join(lines[:lastFunctionClose+1], "\n") + "\n"
}

func setupFunctionLines(t *testing.T, script string) ([]string, int) {
	t.Helper()
	lines := strings.Split(strings.ReplaceAll(script, "\r\n", "\n"), "\n")
	lastFunctionClose := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "}" {
			lastFunctionClose = index
		}
	}
	if lastFunctionClose < 0 {
		t.Fatal("setup script has no function-closing line")
	}
	return lines, lastFunctionClose
}

func psSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
