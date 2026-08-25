package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeKeychainStore struct {
	service string
	user    string
	token   string
	err     error
	sets    []fakeKeychainSet
	deletes []fakeKeychainDelete
}

type fakeKeychainSet struct {
	service  string
	user     string
	password string
}

type fakeKeychainDelete struct {
	service string
	user    string
}

func (f *fakeKeychainStore) Get(service, user string) (string, error) {
	f.service = service
	f.user = user
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

func (f *fakeKeychainStore) Set(service, user, password string) error {
	f.sets = append(f.sets, fakeKeychainSet{
		service:  service,
		user:     user,
		password: password,
	})
	return f.err
}

func (f *fakeKeychainStore) Delete(service, user string) error {
	f.deletes = append(f.deletes, fakeKeychainDelete{
		service: service,
		user:    user,
	})
	return f.err
}

func TestDefaultSourceForGOOS(t *testing.T) {
	tests := []struct {
		goos string
		want Source
	}{
		{goos: "darwin", want: SourceKeychain},
		{goos: "windows", want: SourceKeychain},
		{goos: "linux", want: SourceEnv},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := DefaultSourceForGOOS(tt.goos); got != tt.want {
				t.Fatalf("DefaultSourceForGOOS(%q) = %q, want %q", tt.goos, got, tt.want)
			}
		})
	}
}

func TestAuthConfigPath(t *testing.T) {
	resolver := NewResolver(Runtime{
		GOOS: "windows",
		UserConfigDir: func() (string, error) {
			return filepath.Join("C:", "Users", "alexis", "AppData", "Roaming"), nil
		},
		Getenv: func(string) string { return "" },
	}, nil)

	got, err := resolver.AuthConfigPath()
	if err != nil {
		t.Fatalf("AuthConfigPath() error = %v", err)
	}

	want := filepath.Join("C:", "Users", "alexis", "AppData", "Roaming", "slack-mgmt", "auth.json")
	if got != want {
		t.Fatalf("AuthConfigPath() = %q, want %q", got, want)
	}
}

func TestResolveTokenEnvWinsOverFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
	if err := WriteFileConfig(path, FileConfig{AccessToken: "xoxb-file-token"}); err != nil {
		t.Fatalf("WriteFileConfig() error = %v", err)
	}

	resolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv: func(key string) string {
			if key == AccessTokenEnvVar {
				return "xoxp-env-token"
			}
			return ""
		},
	}, nil)

	got, err := resolver.ResolveToken(ResolveOptions{Source: SourceEnvOrFile})
	if err != nil {
		t.Fatalf("ResolveToken() error = %v", err)
	}
	if got.Token != "xoxp-env-token" || got.ResolvedFrom != "env" {
		t.Fatalf("ResolveToken() = %#v", got)
	}
}

func TestResolveTokenFallsBackToFileProfile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
	if err := WriteFileConfig(path, FileConfig{
		Profiles: map[string]FileProfile{
			"acme": {AccessToken: "xoxb-file-token"},
		},
	}); err != nil {
		t.Fatalf("WriteFileConfig() error = %v", err)
	}

	resolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	got, err := resolver.ResolveToken(ResolveOptions{Source: SourceEnvOrFile, Workspace: "Acme"})
	if err != nil {
		t.Fatalf("ResolveToken() error = %v", err)
	}
	if got.Token != "xoxb-file-token" || got.SectionName != "acme" {
		t.Fatalf("ResolveToken() = %#v", got)
	}
}

func TestResolveTokenFromKeychain(t *testing.T) {
	store := &fakeKeychainStore{token: "xoxb-keychain-token"}
	resolver := NewResolver(Runtime{
		GOOS:          "darwin",
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
		Getenv:        func(string) string { return "" },
	}, store)

	got, err := resolver.ResolveToken(ResolveOptions{
		Source:    SourceKeychain,
		Workspace: "Acme",
	})
	if err != nil {
		t.Fatalf("ResolveToken() error = %v", err)
	}
	if got.Token != "xoxb-keychain-token" {
		t.Fatalf("ResolveToken() token = %q, want keychain token", got.Token)
	}
	if store.service != KeychainServiceName || store.user != "workspace:acme" {
		t.Fatalf("keychain lookup = service:%q user:%q", store.service, store.user)
	}
}

func TestSetAccessEnvOrFileWritesWorkspaceProfile(t *testing.T) {
	tempDir := t.TempDir()
	resolver := NewResolver(Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	result, err := resolver.SetAccess(SetAccessOptions{
		Source:    SourceEnvOrFile,
		Workspace: "Acme",
		Token:     "xoxb-test",
	})
	if err != nil {
		t.Fatalf("SetAccess() error = %v", err)
	}
	if result.SectionName != "acme" {
		t.Fatalf("SetAccess() section = %q, want acme", result.SectionName)
	}

	cfg, err := ReadFileConfig(result.ConfigPath)
	if err != nil {
		t.Fatalf("ReadFileConfig() error = %v", err)
	}
	if got := cfg.Profiles["acme"].AccessToken; got != "xoxb-test" {
		t.Fatalf("profile token = %q, want xoxb-test", got)
	}
}

func TestInspectAccessEnvPrecedenceSkipsFile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
	if err := WriteFileConfig(path, FileConfig{
		Profiles: map[string]FileProfile{
			"acme": {AccessToken: "xoxb-file-token"},
			"beta": {AccessToken: "xoxp-file-token-2"},
		},
	}); err != nil {
		t.Fatalf("WriteFileConfig() error = %v", err)
	}

	resolver := NewResolver(Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv: func(key string) string {
			if key == BotTokenEnvVar {
				return "xoxp-env-token"
			}
			return ""
		},
	}, nil)

	status, err := resolver.InspectAccess(ResolveOptions{Source: SourceEnvOrFile, Workspace: "acme"})
	if err != nil {
		t.Fatalf("InspectAccess() error = %v", err)
	}
	if !status.AccessTokenPresent || status.ResolvedFrom != "env" {
		t.Fatalf("InspectAccess() = %#v", status)
	}
	if len(status.AvailableProfiles) != 0 {
		t.Fatalf("InspectAccess() read lower-precedence file profiles: %#v", status.AvailableProfiles)
	}
}

func TestClearAccessRemovesFileProfile(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
	if err := WriteFileConfig(path, FileConfig{
		Profiles: map[string]FileProfile{
			"acme": {AccessToken: "xoxb-file-token"},
		},
	}); err != nil {
		t.Fatalf("WriteFileConfig() error = %v", err)
	}

	resolver := NewResolver(Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	result, err := resolver.ClearAccess(ResolveOptions{Source: SourceEnvOrFile, Workspace: "acme"})
	if err != nil {
		t.Fatalf("ClearAccess() error = %v", err)
	}
	if !result.Removed {
		t.Fatalf("ClearAccess() removed = false, want true")
	}

	cfg, err := ReadFileConfig(path)
	if err != nil {
		t.Fatalf("ReadFileConfig() error = %v", err)
	}
	if len(cfg.Profiles) != 0 {
		t.Fatalf("profiles still present: %#v", cfg.Profiles)
	}
}

func TestInspectKeychainMissingTokenIsNotFatal(t *testing.T) {
	store := &fakeKeychainStore{err: ErrSecretNotFound}
	resolver := NewResolver(Runtime{
		GOOS:          "darwin",
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
		Getenv:        func(string) string { return "" },
	}, store)

	status, err := resolver.InspectAccess(ResolveOptions{Source: SourceKeychain, Workspace: "acme"})
	if err != nil {
		t.Fatalf("InspectAccess() error = %v", err)
	}
	if status.AccessTokenPresent {
		t.Fatalf("InspectAccess() = %#v, want no token", status)
	}
}

func TestResolveTokenMissingReturnsSentinel(t *testing.T) {
	resolver := NewResolver(Runtime{
		GOOS:          "linux",
		UserConfigDir: func() (string, error) { return t.TempDir(), nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	_, err := resolver.ResolveToken(ResolveOptions{Source: SourceEnvOrFile, Workspace: "acme"})
	if !errors.Is(err, ErrAccessTokenNotFound) {
		t.Fatalf("ResolveToken() error = %v, want ErrAccessTokenNotFound", err)
	}
}

func TestResolveTokenSourcePrecedenceTable(t *testing.T) {
	tests := []struct {
		name         string
		source       Source
		accessEnv    string
		botEnv       string
		wantToken    string
		wantFrom     string
		wantNotFound bool
	}{
		{name: "compat access env wins", source: SourceEnvOrFile, accessEnv: "xoxp-access", botEnv: "xoxb-bot", wantToken: "xoxp-access", wantFrom: "env"},
		{name: "compat bot env beats file", source: SourceEnvOrFile, botEnv: "xoxb-bot", wantToken: "xoxb-bot", wantFrom: "env"},
		{name: "compat falls back to file", source: SourceEnvOrFile, wantToken: "xoxb-file", wantFrom: "file"},
		{name: "explicit file ignores env", source: SourceFile, accessEnv: "xoxp-access", wantToken: "xoxb-file", wantFrom: "file"},
		{name: "explicit env never falls back", source: SourceEnv, wantNotFound: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
			if err := WriteFileConfig(path, FileConfig{Profiles: map[string]FileProfile{
				"acme": {AccessToken: "xoxb-file"},
			}}); err != nil {
				t.Fatalf("WriteFileConfig() error = %v", err)
			}
			resolver := NewResolver(Runtime{
				GOOS:          "linux",
				UserConfigDir: func() (string, error) { return tempDir, nil },
				Getenv: func(key string) string {
					switch key {
					case AccessTokenEnvVar:
						return tt.accessEnv
					case BotTokenEnvVar:
						return tt.botEnv
					default:
						return ""
					}
				},
			}, nil)

			got, err := resolver.ResolveToken(ResolveOptions{Source: tt.source, Workspace: "acme"})
			if tt.wantNotFound {
				if !errors.Is(err, ErrAccessTokenNotFound) {
					t.Fatalf("ResolveToken() error = %v, want ErrAccessTokenNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveToken() error = %v", err)
			}
			if got.Token != tt.wantToken || got.ResolvedFrom != tt.wantFrom {
				t.Fatalf("ResolveToken() token/from = %q/%q, want %q/%q", got.Token, got.ResolvedFrom, tt.wantToken, tt.wantFrom)
			}
		})
	}
}

func TestResolveTokenAutoPlatformDefaultsRejectPlaintextFallback(t *testing.T) {
	tests := []struct {
		name      string
		goos      string
		store     *fakeKeychainStore
		env       string
		wantToken string
		wantFrom  string
		wantErr   error
	}{
		{name: "darwin keychain", goos: "darwin", store: &fakeKeychainStore{token: "xoxb-darwin"}, env: "xoxp-env", wantToken: "xoxb-darwin", wantFrom: "keychain"},
		{name: "windows credential manager", goos: "windows", store: &fakeKeychainStore{token: "xoxb-windows"}, env: "xoxp-env", wantToken: "xoxb-windows", wantFrom: "keychain"},
		{name: "linux environment", goos: "linux", store: &fakeKeychainStore{token: "xoxb-unused"}, env: "xoxp-linux", wantToken: "xoxp-linux", wantFrom: "env"},
		{name: "windows missing secure credential refuses file fallback", goos: "windows", store: &fakeKeychainStore{err: ErrSecretNotFound}, wantErr: ErrAccessTokenNotFound},
		{name: "windows store failure refuses file fallback", goos: "windows", store: &fakeKeychainStore{err: errors.New("backend unavailable")}, wantErr: ErrCredentialStore},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			if err := WriteFileConfig(filepath.Join(tempDir, "slack-mgmt", "auth.json"), FileConfig{
				Profiles: map[string]FileProfile{"acme": {AccessToken: "xoxb-plaintext-must-not-win"}},
			}); err != nil {
				t.Fatalf("WriteFileConfig() error = %v", err)
			}
			resolver := NewResolver(Runtime{
				GOOS:          tt.goos,
				UserConfigDir: func() (string, error) { return tempDir, nil },
				Getenv: func(key string) string {
					if key == AccessTokenEnvVar {
						return tt.env
					}
					return ""
				},
			}, tt.store)

			got, err := resolver.ResolveToken(ResolveOptions{Source: SourceAuto, Workspace: "acme"})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("ResolveToken() error = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveToken() error = %v", err)
			}
			if got.Token != tt.wantToken || got.ResolvedFrom != tt.wantFrom {
				t.Fatalf("ResolveToken() token/from = %q/%q, want %q/%q", got.Token, got.ResolvedFrom, tt.wantToken, tt.wantFrom)
			}
		})
	}
}

func TestAutoSourceUsesPlatformDefaultAcrossAuthLifecycle(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos+" secure store", func(t *testing.T) {
			store := &fakeKeychainStore{token: "xoxb-existing"}
			resolver := NewResolver(Runtime{
				GOOS:          goos,
				UserConfigDir: func() (string, error) { return t.TempDir(), nil },
				Getenv:        func(string) string { return "xoxp-env-must-not-win" },
			}, store)

			if _, err := resolver.SetAccess(SetAccessOptions{Source: SourceAuto, Workspace: "Acme", Token: "xoxb-new"}); err != nil {
				t.Fatalf("SetAccess(auto) error = %v", err)
			}
			if len(store.sets) != 1 || store.sets[0].user != "workspace:acme" {
				t.Fatalf("SetAccess(auto) keychain calls = %#v", store.sets)
			}

			status, err := resolver.InspectAccess(ResolveOptions{Source: SourceAuto, Workspace: "Acme"})
			if err != nil {
				t.Fatalf("InspectAccess(auto) error = %v", err)
			}
			if !status.AccessTokenPresent || status.ResolvedFrom != "keychain" || status.Source != SourceKeychain {
				t.Fatalf("InspectAccess(auto) = %#v", status)
			}

			if _, err := resolver.ClearAccess(ResolveOptions{Source: SourceAuto, Workspace: "Acme"}); err != nil {
				t.Fatalf("ClearAccess(auto) error = %v", err)
			}
			if len(store.deletes) != 1 || store.deletes[0].user != "workspace:acme" {
				t.Fatalf("ClearAccess(auto) keychain calls = %#v", store.deletes)
			}
		})
	}

	t.Run("linux environment refuses persistent mutation", func(t *testing.T) {
		resolver := NewResolver(Runtime{
			GOOS:          "linux",
			UserConfigDir: func() (string, error) { return t.TempDir(), nil },
			Getenv: func(key string) string {
				if key == AccessTokenEnvVar {
					return "xoxp-linux"
				}
				return ""
			},
		}, &fakeKeychainStore{token: "xoxb-keychain-must-not-win"})

		status, err := resolver.InspectAccess(ResolveOptions{Source: SourceAuto, Workspace: "acme"})
		if err != nil {
			t.Fatalf("InspectAccess(auto) error = %v", err)
		}
		if !status.AccessTokenPresent || status.ResolvedFrom != "env" || status.Source != SourceEnv {
			t.Fatalf("InspectAccess(auto) = %#v", status)
		}
		if _, err := resolver.SetAccess(SetAccessOptions{Source: SourceAuto, Token: "xoxb-test"}); !errors.Is(err, ErrEnvironmentWriteUnsupported) {
			t.Fatalf("SetAccess(auto) error = %v, want ErrEnvironmentWriteUnsupported", err)
		}
		if _, err := resolver.ClearAccess(ResolveOptions{Source: SourceAuto}); !errors.Is(err, ErrEnvironmentWriteUnsupported) {
			t.Fatalf("ClearAccess(auto) error = %v, want ErrEnvironmentWriteUnsupported", err)
		}
	})
}

func TestResolveTokenMalformedFileIsFailureNotAbsence(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "slack-mgmt", "auth.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"profiles":`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	_, err := resolver.ResolveToken(ResolveOptions{Source: SourceEnvOrFile, Workspace: "acme"})
	if err == nil || errors.Is(err, ErrAccessTokenNotFound) {
		t.Fatalf("ResolveToken() error = %v, want malformed-file failure", err)
	}
}

func TestResolveTokenPermissionInvalidFileIsFailureNotAbsence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode attack is covered here; Windows DACL enforcement has a platform test")
	}

	tests := []struct {
		name     string
		dirMode  os.FileMode
		fileMode os.FileMode
	}{
		{name: "broad directory mode", dirMode: 0o755, fileMode: 0o600},
		{name: "broad file mode", dirMode: 0o700, fileMode: 0o644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tempDir := t.TempDir()
			dir := filepath.Join(tempDir, "slack-mgmt")
			path := filepath.Join(dir, "auth.json")
			if err := os.Mkdir(dir, tt.dirMode); err != nil {
				t.Fatalf("Mkdir() error = %v", err)
			}
			secret := "xoxb-permission-invalid-secret"
			if err := os.WriteFile(path, []byte(`{"profiles":{"acme":{"access_token":"`+secret+`"}}}`), tt.fileMode); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			resolver := NewResolver(Runtime{
				GOOS:          "linux",
				UserConfigDir: func() (string, error) { return tempDir, nil },
				Getenv:        func(string) string { return "" },
			}, nil)
			_, err := resolver.ResolveToken(ResolveOptions{Source: SourceEnvOrFile, Workspace: "acme"})
			if err == nil || errors.Is(err, ErrAccessTokenNotFound) {
				t.Fatalf("ResolveToken() error = %v, want permission failure", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("ResolveToken() permission error leaked credential: %q", err)
			}
		})
	}
}

func TestWorkspaceProfilesRemainScopedAcrossSetResolveAndClear(t *testing.T) {
	tempDir := t.TempDir()
	resolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv:        func(string) string { return "" },
	}, nil)

	for _, item := range []struct{ workspace, token string }{
		{workspace: " Acme ", token: "xoxb-acme"},
		{workspace: "Beta", token: "xoxp-beta"},
	} {
		if _, err := resolver.SetAccess(SetAccessOptions{Source: SourceFile, Workspace: item.workspace, Token: item.token}); err != nil {
			t.Fatalf("SetAccess(%q) error = %v", item.workspace, err)
		}
	}
	if _, err := resolver.ClearAccess(ResolveOptions{Source: SourceFile, Workspace: "ACME"}); err != nil {
		t.Fatalf("ClearAccess(acme) error = %v", err)
	}
	if _, err := resolver.ResolveToken(ResolveOptions{Source: SourceFile, Workspace: "acme"}); !errors.Is(err, ErrAccessTokenNotFound) {
		t.Fatalf("ResolveToken(acme) error = %v, want not found", err)
	}
	got, err := resolver.ResolveToken(ResolveOptions{Source: SourceFile, Workspace: "beta"})
	if err != nil {
		t.Fatalf("ResolveToken(beta) error = %v", err)
	}
	if got.Token != "xoxp-beta" || got.SectionName != "beta" {
		t.Fatalf("ResolveToken(beta) = %#v", got)
	}
}

func TestExplicitFileDefaultAndNamedInspectionLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	resolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return tempDir, nil },
		Getenv: func(key string) string {
			if key == AccessTokenEnvVar {
				return "xoxp-env-must-not-win"
			}
			return ""
		},
	}, nil)

	for _, item := range []struct{ workspace, token string }{
		{workspace: "", token: "xoxb-default"},
		{workspace: "Acme", token: "xoxp-acme"},
	} {
		if _, err := resolver.SetAccess(SetAccessOptions{Source: SourceFile, Workspace: item.workspace, Token: item.token}); err != nil {
			t.Fatalf("SetAccess(%q) error = %v", item.workspace, err)
		}
	}

	resolved, err := resolver.ResolveToken(ResolveOptions{Source: SourceFile})
	if err != nil || resolved.Token != "xoxb-default" || resolved.ResolvedFrom != "file" {
		t.Fatalf("ResolveToken(default file) = %#v, %v", resolved, err)
	}
	status, err := resolver.InspectAccess(ResolveOptions{Source: SourceFile})
	if err != nil {
		t.Fatalf("InspectAccess(default file) error = %v", err)
	}
	if !status.AccessTokenPresent || status.ResolvedFrom != "file" || status.Source != SourceFile {
		t.Fatalf("InspectAccess(default file) = %#v", status)
	}
	if strings.Join(status.AvailableProfiles, ",") != "acme" {
		t.Fatalf("InspectAccess(default file) profiles = %#v", status.AvailableProfiles)
	}

	missing, err := resolver.InspectAccess(ResolveOptions{Source: SourceFile, Workspace: "beta"})
	if err != nil {
		t.Fatalf("InspectAccess(missing profile) error = %v", err)
	}
	if missing.AccessTokenPresent || missing.ResolvedFrom != "" {
		t.Fatalf("InspectAccess(missing profile) = %#v", missing)
	}
	cleared, err := resolver.ClearAccess(ResolveOptions{Source: SourceFile, Workspace: "beta"})
	if err != nil || cleared.Removed {
		t.Fatalf("ClearAccess(missing profile) = %#v, %v", cleared, err)
	}

	missingPathResolver := NewResolver(Runtime{
		GOOS:          "windows",
		UserConfigDir: func() (string, error) { return filepath.Join(tempDir, "unused"), nil },
	}, nil)
	cleared, err = missingPathResolver.ClearAccess(ResolveOptions{Source: SourceFile, Workspace: "acme"})
	if err != nil || cleared.Removed {
		t.Fatalf("ClearAccess(missing file) = %#v, %v", cleared, err)
	}
}

func TestWriteFileConfigAtomicRestrictiveAndCleansFailure(t *testing.T) {
	t.Run("replacement repairs final mode", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "slack-mgmt", "auth.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(`{"access_token":"xoxb-old"}`), 0o644); err != nil {
			t.Fatalf("WriteFile(old) error = %v", err)
		}
		if err := WriteFileConfig(path, FileConfig{AccessToken: "xoxb-new"}); err != nil {
			t.Fatalf("WriteFileConfig() error = %v", err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("Stat() error = %v", err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("auth config mode = %o, want 600", got)
			}
			dirInfo, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatalf("Stat(config dir) error = %v", err)
			}
			if got := dirInfo.Mode().Perm(); got != 0o700 {
				t.Fatalf("auth config dir mode = %o, want 700", got)
			}
		}
		cfg, err := ReadFileConfig(path)
		if err != nil || cfg.AccessToken != "xoxb-new" {
			t.Fatalf("ReadFileConfig() token/error = %q/%v", cfg.AccessToken, err)
		}
	})

	t.Run("publish failure removes temporary file", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "auth.json")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatalf("Mkdir(target) error = %v", err)
		}
		sentinel := filepath.Join(target, "original-remains")
		if err := os.WriteFile(sentinel, []byte("original"), 0o600); err != nil {
			t.Fatalf("WriteFile(sentinel) error = %v", err)
		}
		if err := WriteFileConfig(target, FileConfig{AccessToken: "xoxb-secret"}); err == nil {
			t.Fatal("WriteFileConfig() unexpectedly replaced a directory")
		}
		if data, err := os.ReadFile(sentinel); err != nil || string(data) != "original" {
			t.Fatalf("failed publication changed original target: data=%q error=%v", data, err)
		}
		entries, err := os.ReadDir(parent)
		if err != nil {
			t.Fatalf("ReadDir() error = %v", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".auth.json.tmp-") {
				t.Fatalf("temporary auth file survived failure: %s", entry.Name())
			}
		}
	})
}

func TestWriteFileConfigFailsClosedWhenProtectionBoundaryFails(t *testing.T) {
	sentinel := errors.New("injected protection failure")
	tests := []struct {
		name              string
		directoryFailure  bool
		wantDirectoryCall int
		wantFileCall      int
	}{
		{name: "directory protection failure", directoryFailure: true, wantDirectoryCall: 1},
		{name: "file protection failure", wantDirectoryCall: 1, wantFileCall: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "slack-mgmt", "auth.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatalf("MkdirAll() error = %v", err)
			}
			original := []byte(`{"access_token":"xoxb-original"}`)
			if err := os.WriteFile(path, original, 0o600); err != nil {
				t.Fatalf("WriteFile(original) error = %v", err)
			}

			previousDirectoryProtector := authDirectoryProtector
			previousFileProtector := authFileProtector
			directoryCalls := 0
			fileCalls := 0
			authDirectoryProtector = func(candidate string) error {
				directoryCalls++
				if tt.directoryFailure {
					return sentinel
				}
				return protectAuthDirectory(candidate)
			}
			authFileProtector = func(candidate string) error {
				fileCalls++
				return sentinel
			}
			t.Cleanup(func() {
				authDirectoryProtector = previousDirectoryProtector
				authFileProtector = previousFileProtector
			})

			err := WriteFileConfig(path, FileConfig{AccessToken: "xoxb-replacement"})
			if !errors.Is(err, sentinel) {
				t.Fatalf("WriteFileConfig() error = %v, want injected protection failure", err)
			}
			if directoryCalls != tt.wantDirectoryCall || fileCalls != tt.wantFileCall {
				t.Fatalf("WriteFileConfig() protection calls = directory:%d file:%d, want %d/%d", directoryCalls, fileCalls, tt.wantDirectoryCall, tt.wantFileCall)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != string(original) {
				t.Fatalf("protection failure changed original config: bytes=%q error=%v", got, readErr)
			}
			entries, readDirErr := os.ReadDir(filepath.Dir(path))
			if readDirErr != nil {
				t.Fatalf("ReadDir() error = %v", readDirErr)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".auth.json.tmp-") {
					t.Fatalf("protection failure left temporary auth config: %s", entry.Name())
				}
			}
		})
	}
}

func TestCredentialStoreErrorsNeverExposeBackendText(t *testing.T) {
	secret := "xoxb-backend-secret"
	for _, operation := range []string{"resolve", "inspect", "set", "clear"} {
		t.Run(operation, func(t *testing.T) {
			store := &fakeKeychainStore{err: errors.New("backend included " + secret)}
			resolver := NewResolver(Runtime{GOOS: "darwin", UserConfigDir: func() (string, error) { return t.TempDir(), nil }}, store)
			var err error
			switch operation {
			case "resolve":
				_, err = resolver.ResolveToken(ResolveOptions{Source: SourceKeychain, Workspace: "acme"})
			case "inspect":
				_, err = resolver.InspectAccess(ResolveOptions{Source: SourceKeychain, Workspace: "acme"})
			case "set":
				_, err = resolver.SetAccess(SetAccessOptions{Source: SourceKeychain, Workspace: "acme", Token: "xoxb-input"})
			case "clear":
				_, err = resolver.ClearAccess(ResolveOptions{Source: SourceKeychain, Workspace: "acme"})
			}
			if !errors.Is(err, ErrCredentialStore) {
				t.Fatalf("operation error = %v, want ErrCredentialStore", err)
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "backend included") {
				t.Fatalf("operation error leaked backend text: %q", err)
			}
		})
	}
}

func TestClassifyAccessTokenRejectsUnsupportedFamilies(t *testing.T) {
	tests := []struct {
		token  string
		family TokenFamily
		ok     bool
	}{
		{token: "xoxb-bot", family: TokenFamilyBot, ok: true},
		{token: "xoxp-user", family: TokenFamilyUser, ok: true},
		{token: "xoxe.xoxb-rotating", family: TokenFamilyRotatingBot, ok: true},
		{token: "xoxe.xoxp-rotating", family: TokenFamilyRotatingUser, ok: true},
		{token: "xapp-app-level"},
		{token: "xwfp-workflow"},
		{token: "xoxe-config-or-refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.token[:4], func(t *testing.T) {
			family, err := ClassifyAccessToken(tt.token)
			if tt.ok {
				if err != nil || family != tt.family {
					t.Fatalf("ClassifyAccessToken() = %q, %v; want %q", family, err, tt.family)
				}
				return
			}
			if !errors.Is(err, ErrUnsupportedTokenFamily) {
				t.Fatalf("ClassifyAccessToken() error = %v, want unsupported", err)
			}
			if strings.Contains(err.Error(), tt.token) {
				t.Fatalf("unsupported error leaked token: %q", err)
			}
		})
	}
}

func TestEnvironmentMutationSourcesRefuse(t *testing.T) {
	resolver := NewResolver(Runtime{GOOS: "linux", UserConfigDir: func() (string, error) { return t.TempDir(), nil }}, nil)
	if _, err := resolver.SetAccess(SetAccessOptions{Source: SourceEnv, Token: "xoxb-test"}); !errors.Is(err, ErrEnvironmentWriteUnsupported) {
		t.Fatalf("SetAccess(env) error = %v", err)
	}
	if _, err := resolver.ClearAccess(ResolveOptions{Source: SourceEnv}); !errors.Is(err, ErrEnvironmentWriteUnsupported) {
		t.Fatalf("ClearAccess(env) error = %v", err)
	}
}
