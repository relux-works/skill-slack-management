package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/relux-works/skill-slack-management/internal/provider"
)

type ReadTransport = provider.TransportKind

const (
	ReadTransportAPI     ReadTransport = provider.TransportAPI
	ReadTransportBrowser ReadTransport = provider.TransportBrowser
)

var (
	ErrUnsupportedReadTransport = errors.New("unsupported_read_transport")
	ErrInvalidBrowserWorkspace  = errors.New("invalid_browser_workspace_url")
	ErrUnsupportedProvider      = errors.New("unsupported_read_provider")
	ErrInvalidProviderConfig    = errors.New("invalid_read_provider_config")
)

type WorkspaceProfile struct {
	Provider      provider.Kind     `json:"provider,omitempty"`
	ReadTransport ReadTransport     `json:"read_transport,omitempty"`
	BrowserURL    string            `json:"browser_url,omitempty"`
	Slackdump     *SlackdumpProfile `json:"slackdump,omitempty"`
}

type SlackdumpProfile struct {
	Executable    string `json:"executable,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	Authorization string `json:"authorization_mode"`
}

type WorkspaceConfig struct {
	Workspaces map[string]WorkspaceProfile `json:"workspaces"`
}

type WorkspaceSelection struct {
	Workspace     string
	ReadTransport ReadTransport
	BrowserURL    string
	ConfigPath    string
	ProviderSpec  provider.ProviderSpec
}

type SetWorkspaceOptions struct {
	Workspace     string
	ReadTransport ReadTransport
	BrowserURL    string
	Provider      provider.Kind
	Slackdump     *SlackdumpProfile
}

func (r *Resolver) WorkspaceConfigPath() (string, error) {
	configDir, err := r.runtime.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(configDir, "slack-mgmt", "workspaces.json"), nil
}

func (r *Resolver) ResolveWorkspace(workspace string) (WorkspaceSelection, error) {
	configPath, err := r.WorkspaceConfigPath()
	if err != nil {
		return WorkspaceSelection{}, err
	}
	normalized := NormalizeWorkspace(workspace)
	selection := WorkspaceSelection{
		Workspace:     normalized,
		ReadTransport: ReadTransportAPI,
		ConfigPath:    configPath,
		ProviderSpec: provider.ProviderSpec{
			Kind:      provider.KindWebAPI,
			Workspace: normalized,
			WebAPI:    provider.WebAPISpec{Transport: provider.TransportAPI},
		},
	}

	cfg, err := ReadWorkspaceConfig(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return selection, nil
	}
	if err != nil {
		return WorkspaceSelection{}, err
	}
	profile, ok := cfg.Workspaces[normalized]
	if !ok {
		return selection, nil
	}
	profile, err = validateWorkspaceProfile(profile)
	if err != nil {
		return WorkspaceSelection{}, fmt.Errorf("workspace %q: %w", normalized, err)
	}
	selection.ReadTransport = profile.ReadTransport
	selection.BrowserURL = profile.BrowserURL
	selection.ProviderSpec = providerSpec(normalized, profile)
	return selection, nil
}

func (r *Resolver) SetWorkspace(opts SetWorkspaceOptions) (WorkspaceSelection, error) {
	configPath, err := r.WorkspaceConfigPath()
	if err != nil {
		return WorkspaceSelection{}, err
	}
	profile, err := validateWorkspaceProfile(WorkspaceProfile{
		Provider:      opts.Provider,
		ReadTransport: opts.ReadTransport,
		BrowserURL:    opts.BrowserURL,
		Slackdump:     opts.Slackdump,
	})
	if err != nil {
		return WorkspaceSelection{}, err
	}

	cfg, err := ReadWorkspaceConfig(configPath)
	if errors.Is(err, os.ErrNotExist) {
		cfg = WorkspaceConfig{Workspaces: map[string]WorkspaceProfile{}}
	} else if err != nil {
		return WorkspaceSelection{}, err
	}
	if cfg.Workspaces == nil {
		cfg.Workspaces = map[string]WorkspaceProfile{}
	}
	normalized := NormalizeWorkspace(opts.Workspace)
	cfg.Workspaces[normalized] = profile
	if err := WriteWorkspaceConfig(configPath, cfg); err != nil {
		return WorkspaceSelection{}, err
	}
	return WorkspaceSelection{
		Workspace:     normalized,
		ReadTransport: profile.ReadTransport,
		BrowserURL:    profile.BrowserURL,
		ConfigPath:    configPath,
		ProviderSpec:  providerSpec(normalized, profile),
	}, nil
}

func ReadWorkspaceConfig(configPath string) (WorkspaceConfig, error) {
	if _, err := os.Stat(configPath); err != nil {
		return WorkspaceConfig{}, err
	}
	if err := validateAuthDirectory(filepath.Dir(configPath)); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("validate workspace config dir: %w", err)
	}
	if err := validateAuthFile(configPath); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("validate workspace config: %w", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("parse workspace config: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var cfg WorkspaceConfig
	if err := decoder.Decode(&cfg); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("parse workspace config: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return WorkspaceConfig{}, fmt.Errorf("parse workspace config: %w", err)
	}
	for workspace, profile := range cfg.Workspaces {
		if NormalizeWorkspace(workspace) != workspace {
			return WorkspaceConfig{}, fmt.Errorf("workspace key %q is not normalized", workspace)
		}
		if _, err := validateWorkspaceProfile(profile); err != nil {
			return WorkspaceConfig{}, fmt.Errorf("workspace %q: %w", workspace, err)
		}
	}
	return cfg, nil
}

func WriteWorkspaceConfig(configPath string, cfg WorkspaceConfig) error {
	return writeProtectedConfig(configPath, cfg, "workspace config")
}

func validateWorkspaceProfile(profile WorkspaceProfile) (WorkspaceProfile, error) {
	profile.Provider = provider.Kind(strings.ToLower(strings.TrimSpace(string(profile.Provider))))
	if profile.Provider == "" {
		profile.Provider = provider.KindWebAPI
	}
	profile.ReadTransport = ReadTransport(strings.ToLower(strings.TrimSpace(string(profile.ReadTransport))))
	switch profile.Provider {
	case provider.KindWebAPI:
		if profile.Slackdump != nil {
			return WorkspaceProfile{}, fmt.Errorf("%w: Web API provider does not accept slackdump settings", ErrInvalidProviderConfig)
		}
		if profile.ReadTransport == "" {
			profile.ReadTransport = ReadTransportAPI
		}
		switch profile.ReadTransport {
		case ReadTransportAPI:
			if strings.TrimSpace(profile.BrowserURL) != "" {
				return WorkspaceProfile{}, fmt.Errorf("%w: api transport does not accept browser_url", ErrInvalidBrowserWorkspace)
			}
			profile.BrowserURL = ""
			return profile, nil
		case ReadTransportBrowser:
			canonical, err := CanonicalBrowserWorkspaceURL(profile.BrowserURL)
			if err != nil {
				return WorkspaceProfile{}, err
			}
			profile.BrowserURL = canonical
			return profile, nil
		default:
			return WorkspaceProfile{}, fmt.Errorf("%w: %q", ErrUnsupportedReadTransport, profile.ReadTransport)
		}
	case provider.KindSlackdump:
		if profile.ReadTransport != "" || strings.TrimSpace(profile.BrowserURL) != "" {
			return WorkspaceProfile{}, fmt.Errorf("%w: Slackdump is a provider and does not accept Web API transport settings", ErrInvalidProviderConfig)
		}
		if profile.Slackdump == nil {
			return WorkspaceProfile{}, fmt.Errorf("%w: Slackdump settings are required", ErrInvalidProviderConfig)
		}
		if strings.IndexFunc(profile.Slackdump.Executable, unicode.IsControl) >= 0 {
			return WorkspaceProfile{}, fmt.Errorf("%w: Slackdump executable path contains control characters", ErrInvalidProviderConfig)
		}
		profile.Slackdump.Executable = strings.TrimSpace(profile.Slackdump.Executable)
		profile.Slackdump.Workspace = strings.TrimSpace(profile.Slackdump.Workspace)
		profile.Slackdump.Authorization = strings.TrimSpace(profile.Slackdump.Authorization)
		if profile.Slackdump.Authorization != provider.SlackdumpExternalOptIn {
			return WorkspaceProfile{}, fmt.Errorf("%w: Slackdump authorization must be %q", ErrInvalidProviderConfig, provider.SlackdumpExternalOptIn)
		}
		return profile, nil
	default:
		return WorkspaceProfile{}, fmt.Errorf("%w: %q", ErrUnsupportedProvider, profile.Provider)
	}
}

func providerSpec(workspace string, profile WorkspaceProfile) provider.ProviderSpec {
	spec := provider.ProviderSpec{Kind: profile.Provider, Workspace: workspace}
	switch profile.Provider {
	case provider.KindSlackdump:
		if profile.Slackdump != nil {
			spec.Slackdump = provider.SlackdumpSpec{
				Executable:    profile.Slackdump.Executable,
				Workspace:     profile.Slackdump.Workspace,
				Authorization: profile.Slackdump.Authorization,
			}
		}
	default:
		spec.WebAPI = provider.WebAPISpec{Transport: profile.ReadTransport, BrowserURL: profile.BrowserURL}
	}
	return spec
}

func CanonicalBrowserWorkspaceURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "app.slack.com") || parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidBrowserWorkspace
	}
	cleanPath := path.Clean(parsed.EscapedPath())
	parts := strings.Split(strings.Trim(cleanPath, "/"), "/")
	if len(parts) < 2 || parts[0] != "client" || !validSlackWorkspaceID(parts[1]) {
		return "", ErrInvalidBrowserWorkspace
	}
	return "https://app.slack.com/client/" + parts[1], nil
}

func validSlackWorkspaceID(value string) bool {
	if len(value) < 9 || len(value) > 32 || value[0] != 'T' {
		return false
	}
	for _, char := range value[1:] {
		if (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate object key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
	}
	if err := walk(); err != nil {
		return err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON token %v", token)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("unexpected trailing JSON value")
	}
	return nil
}
