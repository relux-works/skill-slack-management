package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/relux-works/skill-slack-management/internal/attachments"
	"github.com/relux-works/skill-slack-management/internal/config"
	"github.com/relux-works/skill-slack-management/internal/mutate"
	"github.com/relux-works/skill-slack-management/internal/provider"
	"github.com/relux-works/skill-slack-management/internal/query"
	"github.com/relux-works/skill-slack-management/internal/redact"
	"github.com/relux-works/skill-slack-management/internal/search"
	"github.com/relux-works/skill-slack-management/internal/slack"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"

	cliStdout                   io.Writer = os.Stdout
	cliStderr                   io.Writer = os.Stderr
	cliStdin                    io.Reader = os.Stdin
	exitProcess                           = os.Exit
	redactorLoader                        = redact.LoadDefault
	publishAliasPath                      = defaultPublishAlias
	resolvedSlackClientFactory            = newSlackClientFromResolver
	browserReadTransportFactory           = newBrowserReadTransport
	providerFactoryBuilder                = newRuntimeProviderFactory
	commandContextFactory                 = newCommandContext
)

var ErrCredentialOriginRefused = errors.New("credential_origin_refused")

func newCommandContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

func main() {
	if len(os.Args) < 2 {
		usage()
		exitProcess(2)
		return
	}

	switch os.Args[1] {
	case "auth":
		runAuth(os.Args[2:])
	case "workspace":
		runWorkspace(os.Args[2:])
	case "q":
		runQuery(os.Args[2:])
	case "m":
		runMutate(os.Args[2:])
	case "grep":
		runGrep(os.Args[2:])
	case "attachment":
		runAttachment(os.Args[2:])
	case "version", "--version":
		printVersion()
	case "help", "--help", "-h":
		usage()
	default:
		_ = writeStderr(fmt.Sprintf("unknown command: %s\n\n", os.Args[1]))
		usage()
		exitProcess(2)
	}
}

func usage() {
	_ = writeStderr("Usage:\n" +
		"  slack-mgmt version\n" +
		"  slack-mgmt auth config-path\n" +
		"  slack-mgmt auth set-access (--token TOKEN|--token-stdin) [--workspace NAME] [--source auto|keychain|env|file|env_or_file]\n" +
		"  slack-mgmt auth whoami [--workspace NAME] [--source auto|keychain|env|file|env_or_file] [--check=false] [--base-url URL]\n" +
		"  slack-mgmt auth resolve [--workspace NAME] [--source auto|keychain|env|file|env_or_file]\n" +
		"  slack-mgmt auth clear-access [--workspace NAME] [--source auto|keychain|env|file|env_or_file]\n" +
		"  slack-mgmt workspace config-path\n" +
		"  slack-mgmt workspace set --workspace NAME [--provider web-api|slackdump] [--read-transport api|browser] [--browser-url URL] [--slackdump-executable PATH] [--slackdump-workspace NAME] [--slackdump-authorize-external]\n" +
		"  slack-mgmt workspace show [--workspace NAME]\n" +
		"  slack-mgmt workspace diagnose [--workspace NAME]\n" +
		"  slack-mgmt q '<query>' --format json|compact [--manifest PATH] [--workspace NAME] [--source auto|keychain|env|file|env_or_file] [--base-url URL]\n" +
		"  slack-mgmt m '<mutation>' --format json|compact [--dry-run] [--confirm] [--workspace NAME] [--source auto|keychain|env|file|env_or_file] [--base-url URL]\n" +
		"  slack-mgmt grep 'text query' --format json|compact [--limit N]\n" +
		"  slack-mgmt attachment materialize [--thread-id ID] [--session PATH] [--out-dir DIR] [--manifest PATH] [--format json|compact]\n" +
		"  slack-mgmt attachment stage <id|name|path> --destination PATH [--name FILE] [--manifest PATH] [--force] [--format json|compact]\n")
}

func runWorkspace(args []string) {
	if len(args) < 1 {
		usage()
		exitProcess(2)
		return
	}

	resolver := newResolver()
	switch args[0] {
	case "config-path":
		configPath, err := resolver.WorkspaceConfigPath()
		if err != nil {
			fatalErr(err)
			return
		}
		if err := writeStdout(configPath + "\n"); err != nil {
			fatalErr(err)
		}
	case "set":
		runWorkspaceSet(args[1:], resolver)
	case "show":
		runWorkspaceShow(args[1:], resolver)
	case "diagnose":
		runWorkspaceDiagnose(args[1:], resolver)
	default:
		_ = writeStderr(fmt.Sprintf("unknown workspace command: %s\n\n", args[0]))
		usage()
		exitProcess(2)
	}
}

func runWorkspaceSet(args []string, resolver *config.Resolver) {
	fs := newFlagSet("workspace set")
	workspace := bindWorkspaceFlags(fs)
	providerKind := fs.String("provider", "", "Read provider: web-api or slackdump")
	readTransport := fs.String("read-transport", "", "Web API transport: api or browser")
	browserURL := fs.String("browser-url", "", "Configured https://app.slack.com/client/WORKSPACE_ID URL")
	slackdumpExecutable := fs.String("slackdump-executable", "", "Slackdump executable name or path (arguments are refused)")
	slackdumpWorkspace := fs.String("slackdump-workspace", "", "Slackdump-owned workspace profile name")
	slackdumpAuthorize := fs.Bool("slackdump-authorize-external", false, "Explicitly opt in to Slackdump-owned external authorization")
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}
	if fs.NArg() != 0 {
		fatalErr(fmt.Errorf("workspace set does not accept positional arguments"))
		return
	}
	var slackdumpProfile *config.SlackdumpProfile
	if provider.Kind(*providerKind) == provider.KindSlackdump || *slackdumpExecutable != "" || *slackdumpWorkspace != "" || *slackdumpAuthorize {
		authorization := ""
		if *slackdumpAuthorize {
			authorization = provider.SlackdumpExternalOptIn
		}
		slackdumpProfile = &config.SlackdumpProfile{
			Executable:    *slackdumpExecutable,
			Workspace:     *slackdumpWorkspace,
			Authorization: authorization,
		}
	}
	selection, err := resolver.SetWorkspace(config.SetWorkspaceOptions{
		Workspace:     *workspace,
		Provider:      provider.Kind(*providerKind),
		ReadTransport: config.ReadTransport(*readTransport),
		BrowserURL:    *browserURL,
		Slackdump:     slackdumpProfile,
	})
	if err != nil {
		fatalErr(err)
		return
	}
	if err := writeWorkspaceSelection(selection); err != nil {
		fatalErr(err)
	}
}

func runWorkspaceDiagnose(args []string, resolver *config.Resolver) {
	fs := newFlagSet("workspace diagnose")
	workspace := bindWorkspaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}
	if fs.NArg() != 0 {
		fatalErr(fmt.Errorf("workspace diagnose does not accept positional arguments"))
		return
	}
	readProvider, err := newReadProviderFromResolver(resolver, providerFactoryBuilder(resolver, config.SourceAuto, *workspace, ""), *workspace)
	if err != nil {
		fatalErr(err)
		return
	}
	diagnostic := provider.Diagnostic{Provider: readProvider.Capabilities().Provider, Capabilities: readProvider.Capabilities()}
	if diagnosable, ok := readProvider.(provider.Diagnosable); ok {
		diagnostic = diagnosable.Diagnose(context.Background())
	}
	if err := writeJSON(diagnostic); err != nil {
		fatalErr(err)
	}
}

func runWorkspaceShow(args []string, resolver *config.Resolver) {
	fs := newFlagSet("workspace show")
	workspace := bindWorkspaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}
	if fs.NArg() != 0 {
		fatalErr(fmt.Errorf("workspace show does not accept positional arguments"))
		return
	}
	selection, err := resolver.ResolveWorkspace(*workspace)
	if err != nil {
		fatalErr(err)
		return
	}
	if err := writeWorkspaceSelection(selection); err != nil {
		fatalErr(err)
	}
}

func writeWorkspaceSelection(selection config.WorkspaceSelection) error {
	out := map[string]any{
		"workspace":   selection.Workspace,
		"provider":    selection.ProviderSpec.Kind,
		"config_path": selection.ConfigPath,
	}
	if selection.ProviderSpec.Kind == provider.KindWebAPI {
		out["read_transport"] = string(selection.ReadTransport)
	}
	if selection.BrowserURL != "" {
		out["browser_url"] = selection.BrowserURL
	}
	if selection.ProviderSpec.Kind == provider.KindSlackdump {
		out["slackdump"] = map[string]any{
			"executable":         selection.ProviderSpec.Slackdump.Executable,
			"workspace":          selection.ProviderSpec.Slackdump.Workspace,
			"authorization_mode": selection.ProviderSpec.Slackdump.Authorization,
		}
	}
	return writeJSON(out)
}

func printVersion() {
	_ = writeStdout(fmt.Sprintf("slack-mgmt %s commit=%s build_date=%s %s/%s\n", Version, Commit, BuildDate, runtime.GOOS, runtime.GOARCH))
}

func runAuth(args []string) {
	if len(args) < 1 {
		usage()
		exitProcess(2)
		return
	}

	resolver := newResolver()
	switch args[0] {
	case "config-path":
		path, err := resolver.AuthConfigPath()
		if err != nil {
			fatalErr(err)
			return
		}
		if err := writeStdout(path + "\n"); err != nil {
			fatalErr(err)
		}
	case "set-access":
		runAuthSetAccess(args[1:], resolver)
	case "whoami":
		runAuthWhoAmI(args[1:], resolver)
	case "resolve":
		runAuthResolve(args[1:], resolver)
	case "clear-access", "clean", "clear":
		runAuthClearAccess(args[1:], resolver)
	default:
		_ = writeStderr(fmt.Sprintf("unknown auth command: %s\n\n", args[0]))
		usage()
		exitProcess(2)
	}
}

func runAuthSetAccess(args []string, resolver *config.Resolver) {
	fs := newFlagSet("auth set-access")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	token := bindTokenFlags(fs)
	tokenStdin := fs.Bool("token-stdin", false, "Read the Slack access token from stdin")
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}

	if *tokenStdin && strings.TrimSpace(*token) != "" {
		fatalErr(fmt.Errorf("--token and --token-stdin are mutually exclusive"))
		return
	}
	if *tokenStdin {
		value, err := io.ReadAll(io.LimitReader(cliStdin, 1<<20))
		if err != nil {
			fatalErr(fmt.Errorf("read token from stdin"))
			return
		}
		*token = strings.TrimSpace(string(value))
	}
	if strings.TrimSpace(*token) == "" {
		fatalErr(fmt.Errorf("--token or --token-stdin is required"))
		return
	}

	result, err := resolver.SetAccess(config.SetAccessOptions{
		Source:    config.Source(*source),
		Workspace: *workspace,
		Token:     *token,
	})
	if err != nil {
		fatalErr(err)
		return
	}

	out := map[string]any{
		"source":                  string(result.Source),
		"stored_in":               result.StoredIn,
		"workspace":               result.Workspace,
		"config_path":             result.ConfigPath,
		"token_family":            string(result.TokenFamily),
		"security":                result.Security,
		"plaintext_file_possible": result.PlaintextFilePossible,
		"rotation_supported":      result.RotationSupported,
	}
	if result.AccountKey != "" {
		out["account_key"] = result.AccountKey
	}
	if result.SectionName != "" {
		out["section_name"] = result.SectionName
	}
	if err := writeJSON(out); err != nil {
		fatalErr(err)
	}
}

func runAuthWhoAmI(args []string, resolver *config.Resolver) {
	fs := newFlagSet("auth whoami")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	baseURL := fs.String("base-url", "", "Slack API base URL override")
	check := fs.Bool("check", true, "Perform a live Slack auth.test using the stored credentials")
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}

	status, err := resolver.InspectAccess(config.ResolveOptions{
		Source:    config.Source(*source),
		Workspace: *workspace,
	})
	if err != nil {
		fatalErr(err)
		return
	}

	liveCheck := map[string]any{
		"attempted":              false,
		"ok":                     false,
		"authorization_verified": false,
	}

	if *check && status.AccessTokenPresent {
		liveCheck["attempted"] = true

		client, err := resolvedSlackClientFactory(resolver, config.Source(*source), *workspace, *baseURL)
		if err != nil {
			if errors.Is(err, ErrCredentialOriginRefused) {
				fatalErr(err)
				return
			}
			liveCheck["error"] = err.Error()
		} else {
			authResult, err := client.AuthTest(context.Background())
			if err != nil {
				if errors.Is(err, ErrCredentialOriginRefused) {
					fatalErr(err)
					return
				}
				liveCheck["error"] = err.Error()
			} else {
				liveCheck["ok"] = true
				liveCheck["team"] = authResult.Team
				liveCheck["team_id"] = authResult.TeamID
				liveCheck["user"] = authResult.User
				liveCheck["user_id"] = authResult.UserID
				if authResult.BotID != "" {
					liveCheck["bot_id"] = authResult.BotID
				}
			}
		}
	}

	out := map[string]any{
		"source":                  string(status.Source),
		"stored_in":               status.StoredIn,
		"resolved_from":           status.ResolvedFrom,
		"workspace":               status.Workspace,
		"config_path":             status.ConfigPath,
		"access_token_present":    status.AccessTokenPresent,
		"available_profiles":      status.AvailableProfiles,
		"live_check":              liveCheck,
		"token_family":            string(status.TokenFamily),
		"security":                status.Security,
		"plaintext_file_possible": status.PlaintextFilePossible,
		"rotation_supported":      status.RotationSupported,
	}
	if status.AccountKey != "" {
		out["account_key"] = status.AccountKey
	}
	if status.SectionName != "" {
		out["section_name"] = status.SectionName
	}

	if err := writeJSON(out); err != nil {
		fatalErr(err)
	}
}

func runAuthResolve(args []string, resolver *config.Resolver) {
	fs := newFlagSet("auth resolve")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}

	resolved, err := resolver.ResolveToken(config.ResolveOptions{
		Source:    config.Source(*source),
		Workspace: *workspace,
	})
	if err != nil {
		fatalErr(err)
		return
	}

	out := map[string]any{
		"source":                  string(resolved.Source),
		"resolved_from":           resolved.ResolvedFrom,
		"workspace":               resolved.Workspace,
		"config_path":             resolved.ConfigPath,
		"access_token_present":    resolved.Token != "",
		"token_family":            string(resolved.TokenFamily),
		"security":                resolved.Security,
		"plaintext_file_possible": resolved.PlaintextFilePossible,
		"rotation_supported":      resolved.RotationSupported,
	}
	if resolved.AccountKey != "" {
		out["account_key"] = resolved.AccountKey
	}
	if resolved.SectionName != "" {
		out["section_name"] = resolved.SectionName
	}

	if err := writeJSON(out); err != nil {
		fatalErr(err)
	}
}

func runAuthClearAccess(args []string, resolver *config.Resolver) {
	fs := newFlagSet("auth clear-access")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}

	result, err := resolver.ClearAccess(config.ResolveOptions{
		Source:    config.Source(*source),
		Workspace: *workspace,
	})
	if err != nil {
		fatalErr(err)
		return
	}

	out := map[string]any{
		"source":                  string(result.Source),
		"stored_in":               result.StoredIn,
		"workspace":               result.Workspace,
		"config_path":             result.ConfigPath,
		"removed":                 result.Removed,
		"security":                result.Security,
		"plaintext_file_possible": result.PlaintextFilePossible,
	}
	if result.AccountKey != "" {
		out["account_key"] = result.AccountKey
	}
	if result.SectionName != "" {
		out["section_name"] = result.SectionName
	}

	if err := writeJSON(out); err != nil {
		fatalErr(err)
	}
}

func runQuery(args []string) {
	fs := newFlagSet("q")
	format := fs.String("format", "json", "Output format: json or compact")
	manifestPath := fs.String("manifest", "", "Override attachment manifest path")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	baseURL := fs.String("base-url", "", "Slack API base URL override")
	if err := fs.Parse(reorderFlagArgs(args)); err != nil {
		fatalErr(err)
		return
	}

	if fs.NArg() != 1 {
		fatalErr(fmt.Errorf("q requires exactly one query string argument"))
		return
	}

	resolver := newResolver()
	engine := query.NewEngine(query.Runtime{
		Getenv:             os.Getenv,
		ManifestPath:       *manifestPath,
		PublishAttachment:  publishAttachmentAlias,
		PublishAttachments: publishAttachmentAliases,
		ReadProvider: syncFactory(func() (provider.ReadProvider, error) {
			factory := providerFactoryBuilder(resolver, config.Source(*source), *workspace, *baseURL)
			return newReadProviderFromResolver(resolver, factory, *workspace)
		}),
	})

	ctx, stop := commandContextFactory()
	defer stop()
	results, err := engine.Execute(ctx, fs.Arg(0))
	if err != nil {
		fatalErr(err)
		return
	}

	if err := writeQueryResults(*format, results); err != nil {
		fatalErr(err)
	}
}

func runMutate(args []string) {
	fs := newFlagSet("m")
	format := fs.String("format", "json", "Output format: json or compact")
	dryRun := fs.Bool("dry-run", false, "Validate and render a zero-request mutation plan")
	confirm := fs.Bool("confirm", false, "Confirm destructive delete_message or remove_reaction execution")
	source := bindSourceFlags(fs)
	workspace := bindWorkspaceFlags(fs)
	baseURL := fs.String("base-url", "", "Slack API base URL override")
	if err := fs.Parse(reorderFlagArgs(args)); err != nil {
		fatalErr(err)
		return
	}

	if fs.NArg() != 1 {
		fatalErr(fmt.Errorf("m requires exactly one mutation string argument"))
		return
	}

	resolver := newResolver()
	engine := mutate.NewEngine(mutate.Runtime{
		SlackProvider: syncFactory(func() (mutate.SlackClient, error) {
			return newMutationClientFromResolver(resolver, config.Source(*source), *workspace, *baseURL)
		}),
	})

	var results []query.Result
	var err error
	if *dryRun {
		results, err = engine.Preview(fs.Arg(0))
	} else {
		ctx, stop := commandContextFactory()
		defer stop()
		if *confirm {
			results, err = engine.ExecuteConfirmed(ctx, fs.Arg(0))
		} else {
			results, err = engine.Execute(ctx, fs.Arg(0))
		}
	}
	if err != nil {
		fatalErr(err)
		return
	}

	if err := writeQueryResults(*format, results); err != nil {
		fatalErr(err)
	}
}

func runGrep(args []string) {
	fs := newFlagSet("grep")
	format := fs.String("format", "compact", "Output format: json or compact")
	limit := fs.Int("limit", 20, "Maximum number of matches to return")
	ignoreCase := fs.Bool("ignore-case", true, "Match case-insensitively")
	if err := fs.Parse(reorderFlagArgs(args)); err != nil {
		fatalErr(err)
		return
	}

	if fs.NArg() < 1 {
		fatalErr(fmt.Errorf("grep requires a query string"))
		return
	}

	matches, err := search.Search(search.Options{
		Root:       ".",
		Query:      strings.Join(fs.Args(), " "),
		IgnoreCase: *ignoreCase,
		Limit:      *limit,
		Scopes:     search.DefaultScopes(),
	})
	if err != nil {
		fatalErr(err)
		return
	}

	if err := writeGrepResults(*format, matches); err != nil {
		fatalErr(err)
	}
}

func runAttachment(args []string) {
	if len(args) < 1 {
		usage()
		exitProcess(2)
		return
	}

	switch args[0] {
	case "materialize":
		runAttachmentMaterialize(args[1:])
	case "stage":
		if err := runAttachmentStage(args[1:]); err != nil {
			fatalErr(err)
		}
	default:
		_ = writeStderr(fmt.Sprintf("unknown attachment command: %s\n\n", args[0]))
		usage()
		exitProcess(2)
	}
}

func runAttachmentMaterialize(args []string) {
	fs := newFlagSet("attachment materialize")
	threadID := fs.String("thread-id", "", "Codex thread id override")
	sessionPath := fs.String("session", "", "Rollout session file override")
	outDir := fs.String("out-dir", "", "Output directory for materialized files")
	manifestFlag := fs.String("manifest", "", "Output manifest path")
	format := fs.String("format", "compact", "Output format: json or compact")
	if err := fs.Parse(args); err != nil {
		fatalErr(err)
		return
	}

	opts := attachments.MaterializeOptions{
		ThreadID:     *threadID,
		SessionPath:  *sessionPath,
		OutDir:       *outDir,
		ManifestPath: *manifestFlag,
	}
	resolvedManifestPath, err := runMaterialize(context.Background(), opts)
	if err != nil {
		fatalErr(err)
		return
	}

	loaded, _, err := attachments.LoadManifest(resolvedManifestPath, nil)
	if err != nil {
		fatalErr(err)
		return
	}

	published, err := publishAttachmentAliases(loaded)
	if err != nil {
		fatalErr(err)
		return
	}

	if err := writeAttachmentList(*format, published); err != nil {
		fatalErr(err)
	}
}

func runAttachmentStage(args []string) error {
	fs := newFlagSet("attachment stage")
	destination := fs.String("destination", "", "Destination file path or directory path ending with a path separator")
	name := fs.String("name", "", "Optional destination file name override")
	force := fs.Bool("force", false, "Overwrite destination if it already exists")
	manifestPath := fs.String("manifest", "", "Override attachment manifest path")
	format := fs.String("format", "json", "Output format: json or compact")
	if err := fs.Parse(reorderFlagArgs(args)); err != nil {
		return err
	}

	if fs.NArg() != 1 {
		return fmt.Errorf("attachment stage requires exactly one attachment reference or local file path")
	}
	if strings.TrimSpace(*destination) == "" {
		return fmt.Errorf("--destination is required")
	}

	input, err := attachments.ResolveInput(fs.Arg(0), *manifestPath, os.Getenv)
	if err != nil {
		return err
	}

	result, err := attachments.Stage(input, attachments.StageOptions{
		Destination: *destination,
		Name:        *name,
		Force:       *force,
	})
	if err != nil {
		return err
	}

	alias, err := publishAliasPath(result.DestinationPath)
	if err != nil {
		return err
	}
	return writeStageResult(*format, result, alias)
}

func newResolver() *config.Resolver {
	return config.NewResolver(config.Runtime{
		GOOS:          runtime.GOOS,
		UserConfigDir: os.UserConfigDir,
		Getenv:        os.Getenv,
	}, config.NewDefaultKeychainStore())
}

func newSlackClientFromResolver(resolver *config.Resolver, source config.Source, workspace, baseURL string) (*slack.Client, error) {
	resolved, err := resolver.ResolveToken(config.ResolveOptions{
		Source:    source,
		Workspace: workspace,
	})
	if err != nil {
		if errors.Is(err, config.ErrAccessTokenNotFound) {
			return nil, fmt.Errorf("slack access token not found for workspace %q", config.NormalizeWorkspace(workspace))
		}
		return nil, err
	}
	canonicalBaseURL, err := canonicalStoredCredentialBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	client := *http.DefaultClient
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		if !isSlackAPIURL(request.URL) {
			return ErrCredentialOriginRefused
		}
		return nil
	}
	return slack.NewClientForWorkspace(canonicalBaseURL, resolved.Token, &client, config.NormalizeWorkspace(workspace))
}

func newReadProviderFromResolver(resolver *config.Resolver, factory provider.Factory, workspace string) (provider.ReadProvider, error) {
	selection, err := resolver.ResolveWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if factory == nil {
		return nil, fmt.Errorf("read provider factory is not configured")
	}
	return factory.New(selection.ProviderSpec)
}

type runtimeProviderFactory struct {
	resolver  *config.Resolver
	source    config.Source
	workspace string
	baseURL   string
}

func newRuntimeProviderFactory(resolver *config.Resolver, source config.Source, workspace, baseURL string) provider.Factory {
	return &runtimeProviderFactory{resolver: resolver, source: source, workspace: workspace, baseURL: baseURL}
}

func (f *runtimeProviderFactory) New(spec provider.ProviderSpec) (provider.ReadProvider, error) {
	switch spec.Kind {
	case provider.KindWebAPI:
		if spec.WebAPI.Transport == provider.TransportBrowser {
			if f.source != "" && f.source != config.SourceAuto {
				return nil, fmt.Errorf("browser read transport does not accept --source; configure/authenticate the Chrome workspace tab")
			}
			if strings.TrimSpace(f.baseURL) != "" {
				return nil, fmt.Errorf("browser read transport does not accept --base-url")
			}
		}
		return provider.NewWebAPIProvider(spec.WebAPI.Transport, func() (provider.WebAPITransport, error) {
			switch spec.WebAPI.Transport {
			case provider.TransportAPI:
				return resolvedSlackClientFactory(f.resolver, f.source, f.workspace, f.baseURL)
			case provider.TransportBrowser:
				return browserReadTransportFactory(spec.Workspace, spec.WebAPI.BrowserURL)
			default:
				return nil, config.ErrUnsupportedReadTransport
			}
		})
	case provider.KindSlackdump:
		if f.source != "" && f.source != config.SourceAuto {
			return nil, fmt.Errorf("Slackdump provider does not accept --source; authorization belongs to Slackdump")
		}
		if strings.TrimSpace(f.baseURL) != "" {
			return nil, fmt.Errorf("Slackdump provider does not accept --base-url")
		}
		redactor, err := redactorLoader()
		if err != nil {
			return nil, fmt.Errorf("initialize Slackdump redaction: %w", err)
		}
		return provider.NewSlackdumpProvider(spec.Slackdump, provider.SlackdumpRuntime{Sanitizer: redactor})
	default:
		return nil, config.ErrUnsupportedProvider
	}
}

func newMutationClientFromResolver(resolver *config.Resolver, source config.Source, workspace, baseURL string) (mutate.SlackClient, error) {
	selection, err := resolver.ResolveWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	if selection.ProviderSpec.Kind != provider.KindWebAPI {
		return nil, fmt.Errorf("read_only_provider: workspace %q uses %s; Slack mutations require the Web API provider", selection.Workspace, selection.ProviderSpec.Kind)
	}
	if selection.ReadTransport != config.ReadTransportAPI {
		return nil, fmt.Errorf("%w: workspace %q uses browser reads; Slack mutations require API transport and stored API credentials", slack.ErrBrowserWriteUnsupported, selection.Workspace)
	}
	return resolvedSlackClientFactory(resolver, source, workspace, baseURL)
}

func newBrowserReadTransport(workspace, browserURL string) (provider.WebAPITransport, error) {
	return slack.NewBrowserReadClient(workspace, browserURL, slack.BrowserRuntime{})
}

func canonicalStoredCredentialBaseURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return slack.DefaultBaseURL, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !isSlackAPIURL(parsed) || strings.TrimRight(parsed.Path, "/") != "/api" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrCredentialOriginRefused
	}
	return slack.DefaultBaseURL, nil
}

func isSlackAPIURL(value *url.URL) bool {
	if value == nil || value.Scheme != "https" || !strings.EqualFold(value.Hostname(), "slack.com") || value.Port() != "" || value.User != nil {
		return false
	}
	cleanPath := strings.TrimRight(pathpkg.Clean(value.Path), "/")
	return cleanPath == "/api" || strings.HasPrefix(cleanPath, "/api/")
}

func syncFactory[T any](factory func() (T, error)) func() (T, error) {
	var once sync.Once
	var value T
	var err error

	return func() (T, error) {
		once.Do(func() {
			value, err = factory()
		})
		return value, err
	}
}

func runMaterialize(ctx context.Context, opts attachments.MaterializeOptions) (string, error) {
	args, manifestPath := attachments.BuildMaterializeArgs(opts, os.Getenv)
	cmd := exec.CommandContext(ctx, "agents-attachments", args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("attachment materialize failed")
	}

	resolved, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", fmt.Errorf("resolve manifest path: %w", err)
	}
	return resolved, nil
}

func writeQueryResults(format string, results []query.Result) error {
	switch normalizeFormat(format) {
	case "json":
		return writeJSON(query.JSONValue(results))
	case "compact":
		safeResults, err := sanitizeQueryResults(results)
		if err != nil {
			return err
		}
		rendered, err := query.RenderCompact(safeResults)
		if err != nil {
			return err
		}
		return writePreSanitizedStdout(rendered + "\n")
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func writeGrepResults(format string, matches []search.Match) error {
	switch normalizeFormat(format) {
	case "json":
		return writeJSON(matches)
	case "compact":
		for _, match := range matches {
			if err := writeStdout(fmt.Sprintf("%s:%d:%s\n", match.Path, match.Line, match.Text)); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func writeAttachmentList(format string, list []attachments.Attachment) error {
	items := make([]map[string]any, 0, len(list))
	for _, item := range list {
		items = append(items, query.AttachmentToObject(item))
	}

	switch normalizeFormat(format) {
	case "json":
		return writeJSON(items)
	case "compact":
		return writeQueryResults("compact", []query.Result{{
			Operation: "attachments",
			Kind:      query.ResultKindList,
			Items:     items,
			Columns:   query.DefaultAttachmentPreset(),
		}})
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func writeStageResult(format string, result attachments.StageResult, alias attachments.Alias) error {
	object := map[string]any{
		"id":            result.ID,
		"name":          result.Name,
		"mime_type":     result.MIMEType,
		"size_bytes":    result.SizeBytes,
		"resolved_from": result.ResolvedFrom,
		"local_path":    alias.LocalPath,
		"path_exists":   true,
	}

	switch normalizeFormat(format) {
	case "json":
		return writeJSON(object)
	case "compact":
		return writeQueryResults("compact", []query.Result{{
			Operation: "attachment_stage",
			Kind:      query.ResultKindObject,
			Object:    object,
			Columns:   []string{"id", "name", "mime_type", "size_bytes", "resolved_from", "local_path", "path_exists"},
		}})
	default:
		return fmt.Errorf("unsupported format %q", format)
	}
}

func writeJSON(value any) error {
	redactor, err := redactorLoader()
	if err != nil {
		return fmt.Errorf("initialize output redaction: %w", err)
	}
	safe, err := redactor.Sanitize(value)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(cliStdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(safe)
}

func normalizeFormat(value string) string {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case "", "json":
		return "json"
	case "compact", "llm":
		return "compact"
	default:
		return strings.TrimSpace(strings.ToLower(value))
	}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func defaultPublishAlias(path string) (attachments.Alias, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return attachments.Alias{}, fmt.Errorf("%w: resolve working directory", attachments.ErrAttachmentAlias)
	}
	return attachments.PublishAlias(path, attachments.AliasRuntime{WorkingDir: workingDir})
}

func publishAttachmentAlias(item attachments.Attachment) (attachments.Attachment, error) {
	alias, err := publishAliasPath(item.LocalPath)
	if err != nil {
		return attachments.Attachment{}, err
	}
	item.LocalPath = alias.LocalPath
	item.SizeBytes = alias.SizeBytes
	return item, nil
}

func publishAttachmentAliases(items []attachments.Attachment) ([]attachments.Attachment, error) {
	published := make([]attachments.Attachment, 0, len(items))
	created := make([]attachments.Alias, 0, len(items))
	for _, item := range items {
		alias, err := publishAliasPath(item.LocalPath)
		if err != nil {
			if cleanupErr := removePublishedAliases(created); cleanupErr != nil {
				return nil, cleanupErr
			}
			return nil, err
		}
		created = append(created, alias)
		item.LocalPath = alias.LocalPath
		item.SizeBytes = alias.SizeBytes
		published = append(published, item)
	}
	return published, nil
}

func removePublishedAliases(aliases []attachments.Alias) error {
	for idx := len(aliases) - 1; idx >= 0; idx-- {
		path := aliases[idx].AbsolutePath
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: roll back attachment aliases", attachments.ErrAttachmentAlias)
		}
	}
	return nil
}

func sanitizeQueryResults(results []query.Result) ([]query.Result, error) {
	redactor, err := redactorLoader()
	if err != nil {
		return nil, fmt.Errorf("initialize output redaction: %w", err)
	}
	safe := make([]query.Result, len(results))
	for idx, result := range results {
		safe[idx] = result
		if result.Object != nil {
			safe[idx].Object, err = sanitizeMap(redactor, result.Object)
			if err != nil {
				return nil, err
			}
		}
		if result.Items != nil {
			safe[idx].Items = make([]map[string]any, len(result.Items))
			for itemIdx, item := range result.Items {
				safe[idx].Items[itemIdx], err = sanitizeMap(redactor, item)
				if err != nil {
					return nil, err
				}
			}
		}
		if result.Page != nil {
			safe[idx].Page, err = sanitizeMap(redactor, result.Page)
			if err != nil {
				return nil, err
			}
		}
	}
	return safe, nil
}

func sanitizeMap(redactor *redact.Redactor, value map[string]any) (map[string]any, error) {
	safe, err := redactor.Sanitize(value)
	if err != nil {
		return nil, err
	}
	result, ok := safe.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("redacted output has unexpected type %T", safe)
	}
	return result, nil
}

func writeStdout(value string) error {
	return writeSanitizedText(cliStdout, value)
}

// writePreSanitizedStdout emits transport bytes produced from values that have
// already passed through structured redaction. A second free-text pass would
// reinterpret compact renderer escapes as raw input and change their meaning.
func writePreSanitizedStdout(value string) error {
	_, err := io.WriteString(cliStdout, value)
	return err
}

func writeStderr(value string) error {
	return writeSanitizedText(cliStderr, value)
}

func writeSanitizedText(writer io.Writer, value string) error {
	redactor, err := redactorLoader()
	if err != nil {
		return fmt.Errorf("initialize output redaction: %w", err)
	}
	_, err = fmt.Fprint(writer, redactor.SanitizeText(value))
	return err
}

func reorderFlagArgs(args []string) []string {
	if len(args) < 2 {
		return args
	}

	reordered := make([]string, 0, len(args))
	positionals := make([]string, 0, len(args))

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		if strings.HasPrefix(arg, "-") {
			reordered = append(reordered, arg)
			if !strings.Contains(arg, "=") && idx+1 < len(args) && !strings.HasPrefix(args[idx+1], "-") {
				reordered = append(reordered, args[idx+1])
				idx++
			}
			continue
		}
		positionals = append(positionals, arg)
	}

	return append(reordered, positionals...)
}

func bindSourceFlags(fs *flag.FlagSet) *string {
	source := fs.String("source", string(config.SourceAuto), "Token source: auto, keychain, env, file, env_or_file")
	fs.StringVar(source, "token-source", string(config.SourceAuto), "Deprecated alias for --source")
	return source
}

func bindWorkspaceFlags(fs *flag.FlagSet) *string {
	var workspace string
	fs.StringVar(&workspace, "workspace", "", "Workspace profile label for stored Slack credentials")
	fs.StringVar(&workspace, "profile", "", "Deprecated alias for --workspace")
	return &workspace
}

func bindTokenFlags(fs *flag.FlagSet) *string {
	var token string
	fs.StringVar(&token, "token", "", "Slack access token")
	fs.StringVar(&token, "access-token", "", "Deprecated alias for --token")
	return &token
}

func fatalErr(err error) {
	safe := "redacted error"
	if redactor, loadErr := redactorLoader(); loadErr == nil {
		safe = redactor.SanitizeText(err.Error())
	} else if fallback, fallbackErr := redact.New([]byte("slack-mgmt-emergency-redaction")); fallbackErr == nil {
		safe = "output redaction unavailable: " + fallback.SanitizeText(err.Error())
	}
	_, _ = fmt.Fprintf(cliStderr, "error: %s\n", safe)
	exitProcess(1)
}
