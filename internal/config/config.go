package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const (
	KeychainServiceName = "slack-mgmt"
	AccessTokenEnvVar   = "SLACK_ACCESS_TOKEN"
	BotTokenEnvVar      = "SLACK_BOT_TOKEN"
)

type Source string
type TokenFamily string

const (
	SourceAuto      Source = "auto"
	SourceKeychain  Source = "keychain"
	SourceEnv       Source = "env"
	SourceFile      Source = "file"
	SourceEnvOrFile Source = "env_or_file"

	TokenFamilyBot          TokenFamily = "bot"
	TokenFamilyUser         TokenFamily = "user"
	TokenFamilyRotatingBot  TokenFamily = "rotating_bot"
	TokenFamilyRotatingUser TokenFamily = "rotating_user"
)

type FileConfig struct {
	AccessToken string                 `json:"access_token,omitempty"`
	Profiles    map[string]FileProfile `json:"profiles,omitempty"`
}

type FileProfile struct {
	AccessToken string `json:"access_token,omitempty"`
}

type ResolveOptions struct {
	Source    Source
	Workspace string
}

type SetAccessOptions struct {
	Source    Source
	Workspace string
	Token     string
}

type SetAccessResult struct {
	Source                Source
	StoredIn              string
	ConfigPath            string
	Workspace             string
	AccountKey            string
	SectionName           string
	TokenFamily           TokenFamily
	Security              string
	PlaintextFilePossible bool
	RotationSupported     bool
}

type AccessStatus struct {
	Source                Source
	StoredIn              string
	ResolvedFrom          string
	ConfigPath            string
	Workspace             string
	AccountKey            string
	SectionName           string
	AccessTokenPresent    bool
	AvailableProfiles     []string
	TokenFamily           TokenFamily
	Security              string
	PlaintextFilePossible bool
	RotationSupported     bool
}

type ClearAccessResult struct {
	Source                Source
	StoredIn              string
	ConfigPath            string
	Workspace             string
	AccountKey            string
	SectionName           string
	Removed               bool
	Security              string
	PlaintextFilePossible bool
}

type ResolvedToken struct {
	Token                 string
	Source                Source
	ResolvedFrom          string
	ConfigPath            string
	Workspace             string
	AccountKey            string
	SectionName           string
	TokenFamily           TokenFamily
	Security              string
	PlaintextFilePossible bool
	RotationSupported     bool
}

type Runtime struct {
	GOOS          string
	UserConfigDir func() (string, error)
	Getenv        func(string) string
}

type KeychainStore interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

type Resolver struct {
	runtime  Runtime
	keychain KeychainStore
}

var (
	ErrAccessTokenNotFound         = errors.New("access token not found")
	ErrCredentialStore             = errors.New("credential store operation failed")
	ErrEnvironmentWriteUnsupported = errors.New("environment credentials cannot be changed by a child process")
	ErrUnsupportedTokenFamily      = errors.New("unsupported_token_family")

	authDirectoryProtector = protectAuthDirectory
	authFileProtector      = protectAuthFile
)

func NewResolver(rt Runtime, keychain KeychainStore) *Resolver {
	if rt.GOOS == "" {
		rt.GOOS = runtime.GOOS
	}
	if rt.UserConfigDir == nil {
		rt.UserConfigDir = os.UserConfigDir
	}
	if rt.Getenv == nil {
		rt.Getenv = os.Getenv
	}

	return &Resolver{
		runtime:  rt,
		keychain: keychain,
	}
}

func DefaultSourceForGOOS(goos string) Source {
	if strings.EqualFold(goos, "darwin") || strings.EqualFold(goos, "windows") {
		return SourceKeychain
	}
	return SourceEnv
}

func NormalizeWorkspace(workspace string) string {
	normalized := strings.TrimSpace(strings.ToLower(workspace))
	if normalized == "" {
		return "default"
	}
	return normalized
}

func WorkspaceSectionName(workspace string) string {
	if NormalizeWorkspace(workspace) == "default" {
		return ""
	}
	return NormalizeWorkspace(workspace)
}

func (r *Resolver) AuthConfigPath() (string, error) {
	configDir, err := r.runtime.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config dir: %w", err)
	}
	return filepath.Join(configDir, "slack-mgmt", "auth.json"), nil
}

func (r *Resolver) ResolveToken(opts ResolveOptions) (ResolvedToken, error) {
	source := opts.Source
	if source == "" || source == SourceAuto {
		source = DefaultSourceForGOOS(r.runtime.GOOS)
	}

	switch source {
	case SourceKeychain:
		return r.resolveFromKeychain(opts.Workspace)
	case SourceEnv:
		return r.resolveFromEnv(opts.Workspace)
	case SourceFile:
		return r.resolveFromFile(opts.Workspace)
	case SourceEnvOrFile:
		return r.resolveFromEnvOrFile(opts.Workspace)
	default:
		return ResolvedToken{}, fmt.Errorf("unsupported source %q", source)
	}
}

func (r *Resolver) SetAccess(opts SetAccessOptions) (SetAccessResult, error) {
	source := opts.Source
	if source == "" || source == SourceAuto {
		source = DefaultSourceForGOOS(r.runtime.GOOS)
	}

	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return SetAccessResult{}, fmt.Errorf("token is required")
	}
	family, err := ClassifyAccessToken(token)
	if err != nil {
		return SetAccessResult{}, err
	}

	switch source {
	case SourceKeychain:
		return r.setAccessKeychain(opts.Workspace, token, family)
	case SourceEnv:
		return SetAccessResult{}, ErrEnvironmentWriteUnsupported
	case SourceFile, SourceEnvOrFile:
		return r.setAccessFile(source, opts.Workspace, token, family)
	default:
		return SetAccessResult{}, fmt.Errorf("unsupported source %q", source)
	}
}

func (r *Resolver) InspectAccess(opts ResolveOptions) (AccessStatus, error) {
	source := opts.Source
	if source == "" || source == SourceAuto {
		source = DefaultSourceForGOOS(r.runtime.GOOS)
	}

	switch source {
	case SourceKeychain:
		return r.inspectKeychain(opts.Workspace)
	case SourceEnv:
		return r.inspectEnv(opts.Workspace)
	case SourceFile:
		return r.inspectFile(opts.Workspace)
	case SourceEnvOrFile:
		return r.inspectEnvOrFile(opts.Workspace)
	default:
		return AccessStatus{}, fmt.Errorf("unsupported source %q", source)
	}
}

func (r *Resolver) ClearAccess(opts ResolveOptions) (ClearAccessResult, error) {
	source := opts.Source
	if source == "" || source == SourceAuto {
		source = DefaultSourceForGOOS(r.runtime.GOOS)
	}

	switch source {
	case SourceKeychain:
		return r.clearKeychain(opts.Workspace)
	case SourceEnv:
		return ClearAccessResult{}, ErrEnvironmentWriteUnsupported
	case SourceFile, SourceEnvOrFile:
		return r.clearFile(source, opts.Workspace)
	default:
		return ClearAccessResult{}, fmt.Errorf("unsupported source %q", source)
	}
}

func (r *Resolver) resolveFromKeychain(workspace string) (ResolvedToken, error) {
	accountKey := keychainAccountKey(workspace)
	if r.keychain == nil {
		return ResolvedToken{}, fmt.Errorf("keychain store is not configured")
	}

	token, err := r.keychain.Get(KeychainServiceName, accountKey)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return ResolvedToken{}, ErrAccessTokenNotFound
		}
		return ResolvedToken{}, credentialStoreError("load")
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return ResolvedToken{}, ErrAccessTokenNotFound
	}
	family, err := ClassifyAccessToken(token)
	if err != nil {
		return ResolvedToken{}, err
	}

	path, _ := r.AuthConfigPath()
	security, plaintext := sourceSecurity(SourceKeychain)
	return ResolvedToken{
		Token:                 token,
		Source:                SourceKeychain,
		ResolvedFrom:          "keychain",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		AccountKey:            accountKey,
		TokenFamily:           family,
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}, nil
}

func (r *Resolver) resolveFromEnv(workspace string) (ResolvedToken, error) {
	if envToken := strings.TrimSpace(resolveEnvToken(r.runtime.Getenv)); envToken != "" {
		family, err := ClassifyAccessToken(envToken)
		if err != nil {
			return ResolvedToken{}, err
		}
		path, _ := r.AuthConfigPath()
		security, plaintext := sourceSecurity(SourceEnv)
		return ResolvedToken{
			Token:                 envToken,
			Source:                SourceEnv,
			ResolvedFrom:          "env",
			ConfigPath:            path,
			Workspace:             NormalizeWorkspace(workspace),
			SectionName:           WorkspaceSectionName(workspace),
			TokenFamily:           family,
			Security:              security,
			PlaintextFilePossible: plaintext,
			RotationSupported:     false,
		}, nil
	}
	return ResolvedToken{}, ErrAccessTokenNotFound
}

func (r *Resolver) resolveFromFile(workspace string) (ResolvedToken, error) {
	path, err := r.AuthConfigPath()
	if err != nil {
		return ResolvedToken{}, err
	}

	cfg, err := ReadFileConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ResolvedToken{}, ErrAccessTokenNotFound
		}
		return ResolvedToken{}, err
	}

	workspace = NormalizeWorkspace(workspace)
	if section := WorkspaceSectionName(workspace); section != "" {
		profile, ok := cfg.Profiles[section]
		if !ok || strings.TrimSpace(profile.AccessToken) == "" {
			return ResolvedToken{}, ErrAccessTokenNotFound
		}
		token := strings.TrimSpace(profile.AccessToken)
		family, err := ClassifyAccessToken(token)
		if err != nil {
			return ResolvedToken{}, err
		}
		security, plaintext := sourceSecurity(SourceFile)
		return ResolvedToken{
			Token:                 token,
			Source:                SourceFile,
			ResolvedFrom:          "file",
			ConfigPath:            path,
			Workspace:             workspace,
			SectionName:           section,
			TokenFamily:           family,
			Security:              security,
			PlaintextFilePossible: plaintext,
			RotationSupported:     false,
		}, nil
	}

	if strings.TrimSpace(cfg.AccessToken) == "" {
		return ResolvedToken{}, ErrAccessTokenNotFound
	}
	token := strings.TrimSpace(cfg.AccessToken)
	family, err := ClassifyAccessToken(token)
	if err != nil {
		return ResolvedToken{}, err
	}
	security, plaintext := sourceSecurity(SourceFile)

	return ResolvedToken{
		Token:                 token,
		Source:                SourceFile,
		ResolvedFrom:          "file",
		ConfigPath:            path,
		Workspace:             workspace,
		TokenFamily:           family,
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}, nil
}

func (r *Resolver) resolveFromEnvOrFile(workspace string) (ResolvedToken, error) {
	resolved, err := r.resolveFromEnv(workspace)
	if err == nil {
		resolved.Source = SourceEnvOrFile
		resolved.Security, resolved.PlaintextFilePossible = sourceSecurity(SourceEnvOrFile)
		return resolved, nil
	}
	if !errors.Is(err, ErrAccessTokenNotFound) {
		return ResolvedToken{}, err
	}
	resolved, err = r.resolveFromFile(workspace)
	if err != nil {
		return ResolvedToken{}, err
	}
	resolved.Source = SourceEnvOrFile
	resolved.Security, resolved.PlaintextFilePossible = sourceSecurity(SourceEnvOrFile)
	return resolved, nil
}

func (r *Resolver) setAccessKeychain(workspace, token string, family TokenFamily) (SetAccessResult, error) {
	accountKey := keychainAccountKey(workspace)
	if r.keychain == nil {
		return SetAccessResult{}, fmt.Errorf("keychain store is not configured")
	}

	if err := r.keychain.Set(KeychainServiceName, accountKey, token); err != nil {
		return SetAccessResult{}, credentialStoreError("store")
	}

	path, _ := r.AuthConfigPath()
	security, plaintext := sourceSecurity(SourceKeychain)
	return SetAccessResult{
		Source:                SourceKeychain,
		StoredIn:              "keychain",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		AccountKey:            accountKey,
		TokenFamily:           family,
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}, nil
}

func (r *Resolver) setAccessFile(source Source, workspace, token string, family TokenFamily) (SetAccessResult, error) {
	path, err := r.AuthConfigPath()
	if err != nil {
		return SetAccessResult{}, err
	}

	cfg, err := ReadFileConfig(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return SetAccessResult{}, err
	}

	normalized := NormalizeWorkspace(workspace)
	section := WorkspaceSectionName(normalized)
	if section != "" {
		if cfg.Profiles == nil {
			cfg.Profiles = map[string]FileProfile{}
		}
		cfg.Profiles[section] = FileProfile{AccessToken: token}
	} else {
		cfg.AccessToken = token
	}

	if err := WriteFileConfig(path, cfg); err != nil {
		return SetAccessResult{}, err
	}

	security, plaintext := sourceSecurity(source)
	return SetAccessResult{
		Source:                source,
		StoredIn:              "file",
		ConfigPath:            path,
		Workspace:             normalized,
		SectionName:           section,
		TokenFamily:           family,
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}, nil
}

func (r *Resolver) inspectKeychain(workspace string) (AccessStatus, error) {
	path, _ := r.AuthConfigPath()
	accountKey := keychainAccountKey(workspace)
	security, plaintext := sourceSecurity(SourceKeychain)
	status := AccessStatus{
		Source:                SourceKeychain,
		StoredIn:              "keychain",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		AccountKey:            accountKey,
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}

	if r.keychain == nil {
		return status, fmt.Errorf("keychain store is not configured")
	}

	token, err := r.keychain.Get(KeychainServiceName, accountKey)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return status, nil
		}
		return status, credentialStoreError("inspect")
	}

	token = strings.TrimSpace(token)
	status.AccessTokenPresent = token != ""
	if status.AccessTokenPresent {
		family, err := ClassifyAccessToken(token)
		if err != nil {
			return status, err
		}
		status.ResolvedFrom = "keychain"
		status.TokenFamily = family
	}
	return status, nil
}

func (r *Resolver) inspectEnv(workspace string) (AccessStatus, error) {
	path, err := r.AuthConfigPath()
	if err != nil {
		return AccessStatus{}, err
	}
	security, plaintext := sourceSecurity(SourceEnv)
	status := AccessStatus{
		Source:                SourceEnv,
		StoredIn:              "env",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		SectionName:           WorkspaceSectionName(workspace),
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}
	if envToken := strings.TrimSpace(resolveEnvToken(r.runtime.Getenv)); envToken != "" {
		family, err := ClassifyAccessToken(envToken)
		if err != nil {
			return status, err
		}
		status.AccessTokenPresent = true
		status.ResolvedFrom = "env"
		status.TokenFamily = family
	}
	return status, nil
}

func (r *Resolver) inspectFile(workspace string) (AccessStatus, error) {
	return r.inspectFileAs(SourceFile, workspace)
}

func (r *Resolver) inspectFileAs(source Source, workspace string) (AccessStatus, error) {
	path, err := r.AuthConfigPath()
	if err != nil {
		return AccessStatus{}, err
	}
	security, plaintext := sourceSecurity(source)
	status := AccessStatus{
		Source:                source,
		StoredIn:              "file",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		SectionName:           WorkspaceSectionName(workspace),
		Security:              security,
		PlaintextFilePossible: plaintext,
		RotationSupported:     false,
	}
	cfg, err := ReadFileConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status, nil
		}
		return status, err
	}

	status.AvailableProfiles = profileNames(cfg)
	section := status.SectionName
	var token string
	if section != "" {
		profile, ok := cfg.Profiles[section]
		if ok {
			token = strings.TrimSpace(profile.AccessToken)
		}
	} else {
		token = strings.TrimSpace(cfg.AccessToken)
	}
	status.AccessTokenPresent = token != ""
	if status.AccessTokenPresent {
		family, err := ClassifyAccessToken(token)
		if err != nil {
			return status, err
		}
		status.ResolvedFrom = "file"
		status.TokenFamily = family
	}
	return status, nil
}

func (r *Resolver) inspectEnvOrFile(workspace string) (AccessStatus, error) {
	envStatus, err := r.inspectEnv(workspace)
	if err != nil {
		return AccessStatus{}, err
	}
	if envStatus.AccessTokenPresent {
		envStatus.Source = SourceEnvOrFile
		envStatus.StoredIn = "env_or_file"
		envStatus.Security, envStatus.PlaintextFilePossible = sourceSecurity(SourceEnvOrFile)
		return envStatus, nil
	}

	fileStatus, err := r.inspectFileAs(SourceEnvOrFile, workspace)
	if err != nil {
		return AccessStatus{}, err
	}
	fileStatus.StoredIn = "env_or_file"
	return fileStatus, nil
}

func (r *Resolver) clearKeychain(workspace string) (ClearAccessResult, error) {
	accountKey := keychainAccountKey(workspace)
	if r.keychain == nil {
		return ClearAccessResult{}, fmt.Errorf("keychain store is not configured")
	}

	err := r.keychain.Delete(KeychainServiceName, accountKey)
	if err != nil && !errors.Is(err, ErrSecretNotFound) {
		return ClearAccessResult{}, credentialStoreError("delete")
	}

	path, _ := r.AuthConfigPath()
	security, plaintext := sourceSecurity(SourceKeychain)
	return ClearAccessResult{
		Source:                SourceKeychain,
		StoredIn:              "keychain",
		ConfigPath:            path,
		Workspace:             NormalizeWorkspace(workspace),
		AccountKey:            accountKey,
		Removed:               err == nil,
		Security:              security,
		PlaintextFilePossible: plaintext,
	}, nil
}

func (r *Resolver) clearFile(source Source, workspace string) (ClearAccessResult, error) {
	path, err := r.AuthConfigPath()
	if err != nil {
		return ClearAccessResult{}, err
	}

	cfg, err := ReadFileConfig(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			security, plaintext := sourceSecurity(source)
			return ClearAccessResult{
				Source:                source,
				StoredIn:              "file",
				ConfigPath:            path,
				Workspace:             NormalizeWorkspace(workspace),
				Security:              security,
				PlaintextFilePossible: plaintext,
			}, nil
		}
		return ClearAccessResult{}, err
	}

	normalized := NormalizeWorkspace(workspace)
	section := WorkspaceSectionName(normalized)
	removed := false
	if section != "" {
		if _, ok := cfg.Profiles[section]; ok {
			delete(cfg.Profiles, section)
			removed = true
		}
		if len(cfg.Profiles) == 0 {
			cfg.Profiles = nil
		}
	} else if strings.TrimSpace(cfg.AccessToken) != "" {
		cfg.AccessToken = ""
		removed = true
	}

	if removed {
		if err := WriteFileConfig(path, cfg); err != nil {
			return ClearAccessResult{}, err
		}
	}

	security, plaintext := sourceSecurity(source)
	return ClearAccessResult{
		Source:                source,
		StoredIn:              "file",
		ConfigPath:            path,
		Workspace:             normalized,
		SectionName:           section,
		Removed:               removed,
		Security:              security,
		PlaintextFilePossible: plaintext,
	}, nil
}

func resolveEnvToken(getenv func(string) string) string {
	if getenv == nil {
		return ""
	}
	for _, key := range []string{AccessTokenEnvVar, BotTokenEnvVar} {
		if value := strings.TrimSpace(getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func keychainAccountKey(workspace string) string {
	return "workspace:" + NormalizeWorkspace(workspace)
}

func ReadFileConfig(path string) (FileConfig, error) {
	// Preserve the missing-file contract before checking the containing
	// directory. A missing credential file is absence; an existing file with
	// weak protection is a hard failure and must not trigger source fallback.
	if _, err := os.Stat(path); err != nil {
		return FileConfig{}, err
	}
	if err := validateAuthDirectory(filepath.Dir(path)); err != nil {
		return FileConfig{}, fmt.Errorf("validate auth config dir: %w", err)
	}
	if err := validateAuthFile(path); err != nil {
		return FileConfig{}, fmt.Errorf("validate auth config: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("parse auth config %s: %w", path, err)
	}
	return cfg, nil
}

func WriteFileConfig(path string, cfg FileConfig) error {
	return writeProtectedConfig(path, cfg, "auth config")
}

func writeProtectedConfig(path string, value any, label string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s dir: %w", label, err)
	}

	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", label, err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary %s: %w", label, err)
	}
	tempPath := temp.Name()
	published := false
	defer func() {
		_ = temp.Close()
		if !published {
			_ = os.Remove(tempPath)
		}
	}()

	// Create the empty temporary file before tightening the directory. On
	// Windows this keeps directory and file protection as independent gates:
	// the temporary file inherits the fixture's original DACL and must be
	// explicitly protected before any credential bytes are written.
	if err := authDirectoryProtector(dir); err != nil {
		return fmt.Errorf("protect %s dir: %w", label, err)
	}
	if err := authFileProtector(tempPath); err != nil {
		return fmt.Errorf("protect temporary %s: %w", label, err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary %s: %w", label, err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync temporary %s: %w", label, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary %s: %w", label, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("publish %s: %w", label, err)
	}
	published = true
	if err := validateAuthDirectory(dir); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("validate published %s dir: %w", label, err)
	}
	if err := validateAuthFile(path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("validate published %s: %w", label, err)
	}
	if err := syncAuthDirectory(dir); err != nil {
		return fmt.Errorf("sync %s dir: %w", label, err)
	}
	return nil
}

func ClassifyAccessToken(token string) (TokenFamily, error) {
	value := strings.ToLower(strings.TrimSpace(token))
	switch {
	case strings.HasPrefix(value, "xoxe.xoxb-"):
		return TokenFamilyRotatingBot, nil
	case strings.HasPrefix(value, "xoxe.xoxp-"):
		return TokenFamilyRotatingUser, nil
	case strings.HasPrefix(value, "xoxb-"):
		return TokenFamilyBot, nil
	case strings.HasPrefix(value, "xoxp-"):
		return TokenFamilyUser, nil
	default:
		return "", ErrUnsupportedTokenFamily
	}
}

func credentialStoreError(operation string) error {
	return fmt.Errorf("%s credential store: %w", operation, ErrCredentialStore)
}

func sourceSecurity(source Source) (string, bool) {
	switch source {
	case SourceFile, SourceEnvOrFile:
		return "lower", true
	case SourceKeychain:
		return "secure", false
	default:
		return "environment", false
	}
}

func profileNames(cfg FileConfig) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
