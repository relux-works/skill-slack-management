package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/provider"
)

func TestResolveWorkspaceDefaultsToAPIOnlyWhenConfigIsAbsent(t *testing.T) {
	resolver := workspaceTestResolver(t)

	selection, err := resolver.ResolveWorkspace("Acme")
	if err != nil {
		t.Fatalf("ResolveWorkspace() error = %v", err)
	}
	if selection.Workspace != "acme" || selection.ReadTransport != ReadTransportAPI || selection.BrowserURL != "" {
		t.Fatalf("ResolveWorkspace() = %#v", selection)
	}
}

func TestSetAndResolveWorkspaceBrowserTransport(t *testing.T) {
	resolver := workspaceTestResolver(t)

	set, err := resolver.SetWorkspace(SetWorkspaceOptions{
		Workspace:     " Acme ",
		ReadTransport: ReadTransportBrowser,
		BrowserURL:    "https://app.slack.com/client/T01234567/C999",
	})
	if err != nil {
		t.Fatalf("SetWorkspace() error = %v", err)
	}
	if set.Workspace != "acme" || set.BrowserURL != "https://app.slack.com/client/T01234567" {
		t.Fatalf("SetWorkspace() = %#v", set)
	}

	resolved, err := resolver.ResolveWorkspace("ACME")
	if err != nil {
		t.Fatalf("ResolveWorkspace() error = %v", err)
	}
	if resolved.ReadTransport != ReadTransportBrowser || resolved.BrowserURL != set.BrowserURL {
		t.Fatalf("ResolveWorkspace() = %#v, want %#v", resolved, set)
	}
}

func TestSetAndResolveWorkspaceAPITransport(t *testing.T) {
	resolver := workspaceTestResolver(t)
	set, err := resolver.SetWorkspace(SetWorkspaceOptions{Workspace: "acme", ReadTransport: ReadTransportAPI})
	if err != nil {
		t.Fatalf("SetWorkspace() error = %v", err)
	}
	resolved, err := resolver.ResolveWorkspace("acme")
	if err != nil {
		t.Fatalf("ResolveWorkspace() error = %v", err)
	}
	if set.ReadTransport != ReadTransportAPI || resolved.ReadTransport != ReadTransportAPI || resolved.BrowserURL != "" {
		t.Fatalf("API selections = set:%#v resolved:%#v", set, resolved)
	}
}

func TestSetAndResolveWorkspaceSlackdumpProviderSpec(t *testing.T) {
	resolver := workspaceTestResolver(t)
	selection, err := resolver.SetWorkspace(SetWorkspaceOptions{
		Workspace: "Acme",
		Provider:  provider.KindSlackdump,
		Slackdump: &SlackdumpProfile{
			Executable:    "/opt/Slackdump Tools/slackdump",
			Workspace:     "corp",
			Authorization: provider.SlackdumpExternalOptIn,
		},
	})
	if err != nil {
		t.Fatalf("SetWorkspace() error = %v", err)
	}
	resolved, err := resolver.ResolveWorkspace("ACME")
	if err != nil {
		t.Fatalf("ResolveWorkspace() error = %v", err)
	}
	want := provider.ProviderSpec{
		Kind: provider.KindSlackdump, Workspace: "acme",
		Slackdump: provider.SlackdumpSpec{Executable: "/opt/Slackdump Tools/slackdump", Workspace: "corp", Authorization: provider.SlackdumpExternalOptIn},
	}
	if !reflect.DeepEqual(selection.ProviderSpec, want) || !reflect.DeepEqual(resolved.ProviderSpec, want) {
		t.Fatalf("provider specs = set:%#v resolved:%#v want:%#v", selection.ProviderSpec, resolved.ProviderSpec, want)
	}
	if resolved.ReadTransport != "" || resolved.BrowserURL != "" {
		t.Fatalf("Slackdump leaked Web API routing fields: %#v", resolved)
	}
}

func TestWorkspaceProviderValidationRefusesAmbiguousOrUnapprovedSlackdump(t *testing.T) {
	tests := []struct {
		name string
		opts SetWorkspaceOptions
		want error
	}{
		{
			name: "external authorization absent",
			opts: SetWorkspaceOptions{Provider: provider.KindSlackdump, Slackdump: &SlackdumpProfile{Executable: "slackdump"}},
			want: ErrInvalidProviderConfig,
		},
		{
			name: "mixed with API transport",
			opts: SetWorkspaceOptions{Provider: provider.KindSlackdump, ReadTransport: ReadTransportAPI, Slackdump: &SlackdumpProfile{Authorization: provider.SlackdumpExternalOptIn}},
			want: ErrInvalidProviderConfig,
		},
		{
			name: "Web API with Slackdump settings",
			opts: SetWorkspaceOptions{Provider: provider.KindWebAPI, Slackdump: &SlackdumpProfile{Authorization: provider.SlackdumpExternalOptIn}},
			want: ErrInvalidProviderConfig,
		},
		{
			name: "config supplied control character",
			opts: SetWorkspaceOptions{Provider: provider.KindSlackdump, Slackdump: &SlackdumpProfile{Executable: "slackdump\t--unsafe", Authorization: provider.SlackdumpExternalOptIn}},
			want: ErrInvalidProviderConfig,
		},
		{
			name: "unknown provider",
			opts: SetWorkspaceOptions{Provider: "automatic"},
			want: ErrUnsupportedProvider,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := workspaceTestResolver(t)
			_, err := resolver.SetWorkspace(test.opts)
			if !errors.Is(err, test.want) {
				t.Fatalf("SetWorkspace() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestResolveWorkspaceMalformedConfigNeverFallsBackToAPI(t *testing.T) {
	resolver := workspaceTestResolver(t)
	configPath, err := resolver.WorkspaceConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"workspaces":{"acme":{"read_transport":"browser"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	selection, err := resolver.ResolveWorkspace("acme")
	if err == nil {
		t.Fatalf("ResolveWorkspace() = %#v, want malformed config refusal", selection)
	}
	if !errors.Is(err, ErrInvalidBrowserWorkspace) {
		t.Fatalf("ResolveWorkspace() error = %v, want ErrInvalidBrowserWorkspace", err)
	}
}

func TestReadWorkspaceConfigRefusesDuplicateRoutingEvidence(t *testing.T) {
	resolver := workspaceTestResolver(t)
	configPath, err := resolver.WorkspaceConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := `{"workspaces":{"acme":{"read_transport":"api","read_transport":"browser","browser_url":"https://app.slack.com/client/T01234567"}}}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = resolver.ResolveWorkspace("acme")
	if err == nil || !strings.Contains(err.Error(), "duplicate object key") {
		t.Fatalf("ResolveWorkspace() error = %v, want duplicate-key refusal", err)
	}
}

func TestReadWorkspaceConfigRefusesUnknownTrailingAndNonNormalizedRoutingEvidence(t *testing.T) {
	tests := map[string]string{
		"unknown field":                    `{"workspaces":{"acme":{"read_transport":"api","fallback":"browser"}}}`,
		"unbounded Slackdump archive path": `{"workspaces":{"acme":{"provider":"slackdump","slackdump":{"authorization_mode":"external_opt_in","archive_path":"/tmp/archive"}}}}`,
		"trailing value":                   `{"workspaces":{"acme":{"read_transport":"api"}}} {}`,
		"non-normalized key":               `{"workspaces":{"Acme":{"read_transport":"api"}}}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			resolver := workspaceTestResolver(t)
			configPath, err := resolver.WorkspaceConfigPath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := resolver.ResolveWorkspace("acme"); err == nil {
				t.Fatal("invalid routing config unexpectedly accepted")
			}
		})
	}
}

func TestSetWorkspaceDoesNotOverwriteMalformedExistingConfig(t *testing.T) {
	resolver := workspaceTestResolver(t)
	configPath, err := resolver.WorkspaceConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"workspaces":{"acme":{"read_transport":"broken"}}}`)
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.SetWorkspace(SetWorkspaceOptions{Workspace: "acme", ReadTransport: ReadTransportAPI}); err == nil {
		t.Fatal("SetWorkspace() overwrote malformed routing evidence")
	}
	got, err := os.ReadFile(configPath)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("config after refusal = %q, %v", got, err)
	}
}

func TestSetWorkspaceRejectsInvalidTransportAndBrowserRoutes(t *testing.T) {
	resolver := workspaceTestResolver(t)
	tests := []struct {
		name      string
		transport ReadTransport
		url       string
		want      error
	}{
		{name: "unknown transport", transport: "automatic", want: ErrUnsupportedReadTransport},
		{name: "missing browser URL", transport: ReadTransportBrowser, want: ErrInvalidBrowserWorkspace},
		{name: "wrong origin", transport: ReadTransportBrowser, url: "https://evil.example/client/T01234567", want: ErrInvalidBrowserWorkspace},
		{name: "sensitive query", transport: ReadTransportBrowser, url: "https://app.slack.com/client/T01234567?token=secret", want: ErrInvalidBrowserWorkspace},
		{name: "non workspace id", transport: ReadTransportBrowser, url: "https://app.slack.com/client/C01234567", want: ErrInvalidBrowserWorkspace},
		{name: "lowercase workspace id", transport: ReadTransportBrowser, url: "https://app.slack.com/client/T012abc67", want: ErrInvalidBrowserWorkspace},
		{name: "api with browser URL", transport: ReadTransportAPI, url: "https://app.slack.com/client/T01234567", want: ErrInvalidBrowserWorkspace},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := resolver.SetWorkspace(SetWorkspaceOptions{Workspace: "acme", ReadTransport: test.transport, BrowserURL: test.url})
			if !errors.Is(err, test.want) {
				t.Fatalf("SetWorkspace() error = %v, want %v", err, test.want)
			}
		})
	}
}

func workspaceTestResolver(t *testing.T) *Resolver {
	t.Helper()
	root := t.TempDir()
	return NewResolver(Runtime{
		GOOS:          "darwin",
		UserConfigDir: func() (string, error) { return root, nil },
		Getenv:        func(string) string { return "" },
	}, nil)
}
