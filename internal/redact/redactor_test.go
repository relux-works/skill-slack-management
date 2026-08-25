package redact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/relux-works/skill-slack-management/internal/securefs"
)

func TestRedactorSensitiveValueMatrix(t *testing.T) {
	redactor, err := New([]byte("deterministic-test-salt"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name      string
		input     string
		label     string
		unchanged bool
	}{
		{name: "bot token", input: "xoxb-123456789-secret", label: "token"},
		{name: "user token", input: "xoxp-123456789-secret", label: "token"},
		{name: "rotating access token", input: "xoxe.xoxb-1-fixture", label: "token"},
		{name: "rotating refresh token", input: "xoxe-1-refresh-fixture", label: "token"},
		{name: "app token", input: "xapp-1-fixture", label: "token"},
		{name: "workflow token", input: "xwfp-fixture", label: "token"},
		{name: "bearer header", input: "Authorization: Bearer opaque-value", label: "token"},
		{name: "password", input: "password=hunter2", label: "secret"},
		{name: "secret", input: "secret: swordfish", label: "secret"},
		{name: "api key", input: "api_key=key-fixture", label: "secret"},
		{name: "access token", input: "access_token=access-fixture", label: "token"},
		{name: "action token", input: "action_token=action-fixture", label: "action_token"},
		{name: "email", input: "person@example.test", label: "email"},
		{name: "sensitive URL parameter", input: "https://example.test/path?token=query-fixture&safe=yes", label: "param"},
		{name: "compact international phone", input: "+14155552671", label: "phone"},
		{name: "grouped international phone", input: "+44 20 7946 0958", label: "phone"},
		{name: "parenthesized phone", input: "+1 (415) 555-2671", label: "phone"},
		{name: "North American phone", input: "415-555-2671", label: "phone"},
		{name: "literal newline before extension", input: `+14155552671\next 9`, label: "phone"},
		{name: "literal carriage return before extension", input: `+14155552671\rext 9`, label: "phone"},
		{name: "literal tab before extension", input: `+14155552671\text 9`, label: "phone"},
		{name: "literal newline before compact extension", input: `+14155552671\nx123`, label: "phone"},
		{name: "literal carriage return before compact extension", input: `+14155552671\rx123`, label: "phone"},
		{name: "literal tab before compact extension", input: `+14155552671\tx123`, label: "phone"},
		{name: "channel ID", input: "C0123456789", unchanged: true},
		{name: "DM ID", input: "D0123456789", unchanged: true},
		{name: "team ID", input: "T0123456789", unchanged: true},
		{name: "user ID", input: "U0123456789", unchanged: true},
		{name: "workspace ID", input: "W0123456789", unchanged: true},
		{name: "bot ID", input: "B0123456789", unchanged: true},
		{name: "timestamp", input: "1710000005.000600", unchanged: true},
		{name: "bare digits", input: "1234567", unchanged: true},
		{name: "invalid leading zero", input: "+01234567890", unchanged: true},
		{name: "left boundary fails", input: "x+14155552671", unchanged: true},
		{name: "right boundary fails", input: "+14155552671x", unchanged: true},
		{name: "too short grouped digits", input: "1-23-456", unchanged: true},
		{name: "Unicode digits", input: "+١٤١٥٥٥٥٢٦٧١", unchanged: true},
		{name: "extension", input: "+14155552671 ext 9", unchanged: true},
		{name: "compact extension", input: "+14155552671 x123", unchanged: true},
		{name: "tab extension", input: "+14155552671\text 9", unchanged: true},
		{name: "newline extension", input: "+14155552671\next 9", unchanged: true},
		{name: "tab compact extension", input: "+14155552671\tx123", unchanged: true},
		{name: "newline compact extension", input: "+14155552671\nx123", unchanged: true},
	}

	markerPattern := regexp.MustCompile(`^<[a-z_]+:[0-9a-f]{10}>$`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactor.SanitizeText(tt.input)
			if tt.unchanged {
				if got != tt.input {
					t.Fatalf("SanitizeText(%q) = %q, want unchanged", tt.input, got)
				}
				return
			}
			if strings.Contains(got, tt.input) {
				t.Fatalf("SanitizeText(%q) leaked raw value in %q", tt.input, got)
			}
			markerStart := strings.Index(got, "<"+tt.label+":")
			if markerStart < 0 {
				t.Fatalf("SanitizeText(%q) = %q, want %s marker", tt.input, got, tt.label)
			}
			markerEnd := strings.Index(got[markerStart:], ">")
			if markerEnd < 0 || !markerPattern.MatchString(got[markerStart:markerStart+markerEnd+1]) {
				t.Fatalf("SanitizeText(%q) has malformed marker in %q", tt.input, got)
			}
		})
	}

}

func TestRedactorMarkersAreDeterministicWithinSaltScope(t *testing.T) {
	first, _ := New([]byte("same-salt"))
	second, _ := New([]byte("same-salt"))
	other, _ := New([]byte("other-salt"))

	value := "person@example.test"
	if first.Marker("email", value) != second.Marker("email", value) {
		t.Fatal("equal label/value pairs under one salt produced different markers")
	}
	if first.Marker("email", value) == first.Marker("email", "other@example.test") {
		t.Fatal("different values deliberately collapsed")
	}
	if first.Marker("email", value) == first.Marker("phone", value) {
		t.Fatal("different labels deliberately collapsed")
	}
	if first.Marker("email", value) == other.Marker("email", value) {
		t.Fatal("different salt scopes produced the same marker")
	}
}

func TestRedactorRejectsEmptySaltAndPreservesExistingMarkers(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) error = nil, want refusal")
	}
	redactor, _ := New([]byte("marker-salt"))
	marker := redactor.Marker("token", "fixture")
	input := marker + " person@example.test " + marker
	got := redactor.SanitizeText(input)
	if strings.Count(got, marker) != 2 || strings.Contains(got, "person@example.test") {
		t.Fatalf("SanitizeText(existing markers) = %q", got)
	}
	if got := redactor.Marker("", "fixture"); !strings.HasPrefix(got, "<secret:") {
		t.Fatalf("Marker(empty label) = %q, want secret label", got)
	}
}

func TestRedactorExplicitSensitiveContextsRejectSelfMintedMarkers(t *testing.T) {
	redactor, _ := New([]byte("marker-context-salt"))
	forged := fmt.Sprintf("<token:%010x>", 0x12345)
	tests := []struct {
		name      string
		input     string
		wantLabel string
	}{
		{name: "sensitive query parameter", input: "https://example.test/?token=" + forged, wantLabel: "param"},
		{name: "quoted password", input: `password="` + forged + `"`, wantLabel: "secret"},
		{name: "unquoted password", input: "password=" + forged, wantLabel: "secret"},
		{name: "bearer authorization", input: "Authorization: Bearer " + forged, wantLabel: "token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactor.SanitizeText(tt.input)
			if strings.Contains(got, forged) || !strings.Contains(got, "<"+tt.wantLabel+":") {
				t.Fatalf("SanitizeText(self-minted marker context) = %q, want new %s marker", got, tt.wantLabel)
			}
		})
	}
}

func TestRedactorSanitizeHandlesNestedArraysAndEncodeErrors(t *testing.T) {
	redactor, _ := New([]byte("nested-salt"))
	value := []any{
		map[string]any{"password": "two words", "safe": true},
		map[string]any{"phone": "internal extension", "count": json.Number("7")},
		nil,
	}
	sanitized, err := redactor.Sanitize(value)
	if err != nil {
		t.Fatalf("Sanitize(nested) error = %v", err)
	}
	got := fmt.Sprintf("%v", sanitized)
	for _, raw := range []string{"two words", "internal extension"} {
		if strings.Contains(got, raw) {
			t.Fatalf("Sanitize(nested) leaked %q in %q", raw, got)
		}
	}
	if _, err := redactor.Sanitize(func() {}); err == nil {
		t.Fatal("Sanitize(function) error = nil, want encode refusal")
	}
}

func TestRedactorClassifiesStructuredFieldsBeforeFreeText(t *testing.T) {
	redactor, _ := New([]byte("structured-salt"))
	value := map[string]any{
		"email":        "not-an-email",
		"phone_number": "internal-extension",
		"action_token": "opaque action value",
		"safe":         "C0123456789 1710000005.000600",
	}

	sanitized, err := redactor.Sanitize(value)
	if err != nil {
		t.Fatalf("Sanitize() error = %v", err)
	}
	got := fmt.Sprintf("%v", sanitized)
	for _, raw := range []string{"not-an-email", "internal-extension", "opaque action value"} {
		if strings.Contains(got, raw) {
			t.Fatalf("Sanitize() leaked %q in %q", raw, got)
		}
	}
	for _, safe := range []string{"C0123456789", "1710000005.000600"} {
		if !strings.Contains(got, safe) {
			t.Fatalf("Sanitize() removed structural value %q from %q", safe, got)
		}
	}
}

func TestRedactorClassifiesSensitiveStructuredValuesBeforeTypeDispatch(t *testing.T) {
	redactor, _ := New([]byte("structured-type-salt"))
	tests := []struct {
		name      string
		field     string
		value     any
		label     string
		canonical string
	}{
		{name: "number", field: "api_key", value: json.Number("8675309"), label: "secret", canonical: "8675309"},
		{name: "boolean", field: "access_token", value: true, label: "token", canonical: "true"},
		{name: "array", field: "password", value: []any{"first", json.Number("7")}, label: "secret", canonical: `["first",7]`},
		{name: "object", field: "action_token", value: map[string]any{"second": false, "first": "value"}, label: "action_token", canonical: `{"first":"value","second":false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sanitized, err := redactor.Sanitize(map[string]any{tt.field: tt.value})
			if err != nil {
				t.Fatalf("Sanitize(%s) error = %v", tt.name, err)
			}
			got, ok := sanitized.(map[string]any)[tt.field].(string)
			if !ok {
				t.Fatalf("Sanitize(%s) value type = %T, want deterministic marker string", tt.name, sanitized.(map[string]any)[tt.field])
			}
			want := redactor.Marker(tt.label, tt.canonical)
			if got != want {
				t.Fatalf("Sanitize(%s) value = %q, want %q", tt.name, got, want)
			}
		})
	}

	absent, err := redactor.Sanitize(map[string]any{"api_key": nil, "password": ""})
	if err != nil {
		t.Fatalf("Sanitize(absent values) error = %v", err)
	}
	absentMap := absent.(map[string]any)
	if absentMap["api_key"] != nil || absentMap["password"] != "" {
		t.Fatalf("Sanitize(absent values) = %#v, want null and empty string preserved", absentMap)
	}
}

func TestRedactorSanitizesSensitiveMapKeysAndRejectsCollisions(t *testing.T) {
	redactor, _ := New([]byte("structured-key-salt"))
	rawKey := "person@example.test"
	marker := redactor.Marker("email", rawKey)

	sanitized, err := redactor.Sanitize(map[string]any{
		"metadata": map[string]any{
			rawKey:    "safe value",
			"channel": "C0123456789",
			"ts":      "1710000005.000600",
		},
	})
	if err != nil {
		t.Fatalf("Sanitize(sensitive key) error = %v", err)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(sanitized); err != nil {
		t.Fatalf("Encode(sanitized) error = %v", err)
	}
	got := encoded.String()
	if strings.Contains(got, rawKey) || !strings.Contains(got, marker) {
		t.Fatalf("Sanitize(sensitive key) = %s, want deterministic marker without raw key", got)
	}
	for _, structural := range []string{"C0123456789", "1710000005.000600"} {
		if !strings.Contains(got, structural) {
			t.Fatalf("Sanitize(sensitive key) removed structural value %q from %s", structural, got)
		}
	}

	_, err = redactor.Sanitize(map[string]any{
		rawKey: "first",
		marker: "second",
	})
	if err == nil || !strings.Contains(err.Error(), "key collision") {
		t.Fatalf("Sanitize(colliding keys) error = %v, want fail-closed key collision", err)
	}
	if strings.Contains(err.Error(), rawKey) {
		t.Fatalf("Sanitize(colliding keys) error leaked raw key: %q", err)
	}
}

func TestRedactorSpacedCredentialLabelsStayBounded(t *testing.T) {
	redactor, _ := New([]byte("spaced-label-salt"))
	tests := []struct {
		name       string
		input      string
		wantLabel  string
		wantChange bool
	}{
		{name: "API key", input: "API key: api-secret", wantLabel: "secret", wantChange: true},
		{name: "access token", input: "access token=access-secret", wantLabel: "token", wantChange: true},
		{name: "refresh token", input: "refresh token: refresh-secret", wantLabel: "token", wantChange: true},
		{name: "action token", input: "action token: action-secret", wantLabel: "action_token", wantChange: true},
		{name: "API keynote", input: "API keynote: public-value"},
		{name: "access tokenization", input: "access tokenization: public-value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactor.SanitizeText(tt.input)
			if !tt.wantChange {
				if got != tt.input {
					t.Fatalf("SanitizeText(%q) = %q, want unchanged adjacent negative", tt.input, got)
				}
				return
			}
			if got == tt.input || !strings.Contains(got, "<"+tt.wantLabel+":") {
				t.Fatalf("SanitizeText(%q) = %q, want %s marker", tt.input, got, tt.wantLabel)
			}
		})
	}
}

func TestRedactorClassifiesBoundedStructuredCredentialFields(t *testing.T) {
	redactor, _ := New([]byte("structured-field-salt"))
	tests := []struct {
		name      string
		field     string
		wantLabel string
	}{
		{name: "exact password", field: "password", wantLabel: "secret"},
		{name: "prefixed password", field: "database-password", wantLabel: "secret"},
		{name: "client secret", field: "client_secret", wantLabel: "secret"},
		{name: "spaced api key", field: "API key", wantLabel: "secret"},
		{name: "prefixed api key", field: "service-api_key", wantLabel: "secret"},
		{name: "access token", field: "access_token", wantLabel: "token"},
		{name: "prefixed refresh token", field: "oauth refresh-token", wantLabel: "token"},
		{name: "action token", field: "action-token", wantLabel: "action_token"},
		{name: "generic token suffix", field: "bot_token", wantLabel: "token"},
		{name: "adjacent secretary", field: "secretary"},
		{name: "password hint", field: "password_hint"},
		{name: "api keynote", field: "api keynote"},
		{name: "access tokenization", field: "access tokenization"},
		{name: "public key", field: "public_key"},
		{name: "repeated separator", field: "client__secret"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const raw = "opaque structured value"
			sanitized, err := redactor.Sanitize(map[string]any{tt.field: raw})
			if err != nil {
				t.Fatalf("Sanitize(%q) error = %v", tt.field, err)
			}
			got := sanitized.(map[string]any)[tt.field].(string)
			if tt.wantLabel == "" {
				if got != raw {
					t.Fatalf("field %q value = %q, want unchanged adjacent negative", tt.field, got)
				}
				return
			}
			if got == raw || !strings.HasPrefix(got, "<"+tt.wantLabel+":") {
				t.Fatalf("field %q value = %q, want %s marker", tt.field, got, tt.wantLabel)
			}
		})
	}
}

func TestRedactorSensitiveQueryNamesDecodeExactlyOnce(t *testing.T) {
	redactor, _ := New([]byte("encoded-query-salt"))
	tests := []struct {
		name       string
		input      string
		wantMarker bool
	}{
		{name: "literal api key", input: "https://example.test/?api_key=literal-fixture", wantMarker: true},
		{name: "encoded underscore", input: "https://example.test/?api%5Fkey=encoded-fixture", wantMarker: true},
		{name: "encoded first letter", input: "https://example.test/?%61ccess_token=access-fixture", wantMarker: true},
		{name: "encoded action token", input: "https://example.test/?action%5Ftoken=action-fixture", wantMarker: true},
		{name: "double encoded name", input: "https://example.test/?%2561pi_key=public-fixture"},
		{name: "malformed encoded name", input: "https://example.test/?api%ZZkey=public-fixture"},
		{name: "safe name", input: "https://example.test/?monkey=public-fixture"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactor.SanitizeText(tt.input)
			if tt.wantMarker {
				if got == tt.input || !strings.Contains(got, "<param:") {
					t.Fatalf("SanitizeText(%q) = %q, want param marker", tt.input, got)
				}
				return
			}
			if got != tt.input {
				t.Fatalf("SanitizeText(%q) = %q, want unchanged single-decode negative", tt.input, got)
			}
		})
	}
}

func TestLoadOrCreateSaltIsStableAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "slack-mgmt", "redaction-salt")
	random := bytes.NewReader(bytes.Repeat([]byte{0x42}, saltBytes))
	first, err := LoadOrCreate(path, random)
	if err != nil {
		t.Fatalf("LoadOrCreate(first) error = %v", err)
	}
	second, err := LoadOrCreate(path, bytes.NewReader(bytes.Repeat([]byte{0x99}, saltBytes)))
	if err != nil {
		t.Fatalf("LoadOrCreate(second) error = %v", err)
	}
	if first.Marker("token", "fixture") != second.Marker("token", "fixture") {
		t.Fatal("stored installation salt was not stable")
	}
	if err := securefs.ValidateDirectory(filepath.Dir(path)); err != nil {
		t.Fatalf("salt directory is not current-user-only: %v", err)
	}
	if err := securefs.ValidateFile(path); err != nil {
		t.Fatalf("salt is not current-user-only: %v", err)
	}
}

func TestLoadOrCreateSaltRejectsEmptyExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redaction-salt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := LoadOrCreate(path, bytes.NewReader(bytes.Repeat([]byte{1}, saltBytes))); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("LoadOrCreate(empty) error = %v, want empty-salt refusal", err)
	}
}

func TestLoadOrCreateSaltRejectsNonRegularExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redaction-salt")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if _, err := LoadOrCreate(path, nil); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("LoadOrCreate(directory) error = %v, want non-regular refusal", err)
	}
}
