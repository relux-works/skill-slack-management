package redact

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var (
	queryParameterPattern = regexp.MustCompile(`([?&])([^=&#\s]+)(=)([^&#\s]+)`)
	labeledSecretPattern  = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|action[ _-]?token|token)\b(\s*[:=]\s*)("[^"]*"|'[^']*'|<[a-z_]+:[0-9a-f]{10}>|[^\s,;'"<>]+)`)
	bearerPattern         = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer\s+)(<[a-z_]+:[0-9a-f]{10}>|[^\s,;"'<>]+)`)
	slackTokenPattern     = regexp.MustCompile(`(?i)\b(?:xox[bp]-[a-z0-9._-]+|xoxe(?:[.-][a-z0-9_-]+)+|xapp-[a-z0-9._-]+|xwfp-[a-z0-9._-]+)\b`)
	emailPattern          = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	phonePattern          = regexp.MustCompile(`(?:\+?[1-9][0-9]{0,2}[ .-]\([0-9]{2,4}\)[ .-][0-9]{3,4}[ .-][0-9]{3,4}|\+1[ .-][2-9][0-9]{2}[ .-][0-9]{3}[ .-][0-9]{4}|[2-9][0-9]{2}[ .-][0-9]{3}[ .-][0-9]{4}|\+[1-9][0-9]{0,2}(?:[ .-][0-9]{2,6}){2,5}|\+[1-9][0-9]{6,14})`)
	markerPattern         = regexp.MustCompile(`<[a-z_]+:[0-9a-f]{10}>`)
)

// Redactor deterministically pseudonymizes sensitive output under one salt.
// It is safe to reuse concurrently after construction.
type Redactor struct {
	salt []byte
}

func New(salt []byte) (*Redactor, error) {
	if len(salt) == 0 {
		return nil, fmt.Errorf("redaction salt is empty")
	}
	return &Redactor{salt: append([]byte(nil), salt...)}, nil
}

// Marker implements <label:first-10-lowercase-hex(HMAC-SHA256(salt,
// label || NUL || trimmed-value))>.
func (r *Redactor) Marker(label, value string) string {
	label = normalizeLabel(label)
	value = strings.TrimSpace(value)
	mac := hmac.New(sha256.New, r.salt)
	_, _ = mac.Write([]byte(label))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	digest := hex.EncodeToString(mac.Sum(nil))
	return "<" + label + ":" + digest[:10] + ">"
}

// Sanitize converts arbitrary JSON-compatible values into a sanitized deep
// copy. Structured field names are classified before strings are scanned.
func (r *Redactor) Sanitize(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode value for redaction: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode value for redaction: %w", err)
	}
	return r.sanitizeNode("", decoded)
}

func (r *Redactor) sanitizeNode(field string, value any) (any, error) {
	if label := labelForField(field); label != "" {
		switch typed := value.(type) {
		case nil:
			return nil, nil
		case string:
			if strings.TrimSpace(typed) == "" {
				return typed, nil
			}
			return r.Marker(label, typed), nil
		default:
			encoded, err := json.Marshal(typed)
			if err != nil {
				return nil, fmt.Errorf("encode sensitive structured value: %w", err)
			}
			return r.Marker(label, string(encoded)), nil
		}
	}

	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			safeKey := r.SanitizeText(key)
			if _, exists := out[safeKey]; exists {
				return nil, fmt.Errorf("redaction key collision")
			}
			item, err := r.sanitizeNode(key, typed[key])
			if err != nil {
				return nil, err
			}
			out[safeKey] = item
		}
		return out, nil
	case []any:
		out := make([]any, len(typed))
		for idx, item := range typed {
			safe, err := r.sanitizeNode(field, item)
			if err != nil {
				return nil, err
			}
			out[idx] = safe
		}
		return out, nil
	case string:
		return r.SanitizeText(typed), nil
	default:
		return value, nil
	}
}

// SanitizeText conservatively scans free text while preserving Slack resource
// IDs and timestamps that are not explicitly classified as credential data.
func (r *Redactor) SanitizeText(input string) string {
	if input == "" {
		return input
	}
	spans := explicitSensitiveSpans(input)
	if len(spans) == 0 {
		return r.sanitizeImplicitText(input)
	}
	var out strings.Builder
	previous := 0
	for _, span := range spans {
		if span.start < previous {
			continue
		}
		out.WriteString(r.sanitizeImplicitText(input[previous:span.start]))
		out.WriteString(r.Marker(span.label, span.value))
		previous = span.end
	}
	out.WriteString(r.sanitizeImplicitText(input[previous:]))
	return out.String()
}

// sanitizeImplicitText preserves marker syntax only after explicit credential
// contexts have been classified from the caller's original text. Marker shape
// alone is not evidence that a query parameter, labeled secret, or Bearer value
// has already crossed this redaction boundary.
func (r *Redactor) sanitizeImplicitText(input string) string {
	indices := markerPattern.FindAllStringIndex(input, -1)
	if len(indices) == 0 {
		return r.sanitizeUnmarkedText(input)
	}
	var out strings.Builder
	previous := 0
	for _, index := range indices {
		out.WriteString(r.sanitizeUnmarkedText(input[previous:index[0]]))
		out.WriteString(input[index[0]:index[1]])
		previous = index[1]
	}
	out.WriteString(r.sanitizeUnmarkedText(input[previous:]))
	return out.String()
}

func (r *Redactor) sanitizeUnmarkedText(input string) string {
	text := slackTokenPattern.ReplaceAllStringFunc(input, func(match string) string {
		return r.Marker("token", match)
	})
	text = emailPattern.ReplaceAllStringFunc(text, func(match string) string {
		return r.Marker("email", match)
	})
	return r.redactPhones(text)
}

type sensitiveSpan struct {
	start    int
	end      int
	priority int
	label    string
	value    string
}

func explicitSensitiveSpans(input string) []sensitiveSpan {
	spans := make([]sensitiveSpan, 0)
	for _, indices := range queryParameterPattern.FindAllStringSubmatchIndex(input, -1) {
		name, err := url.QueryUnescape(input[indices[4]:indices[5]])
		if err != nil || !isSensitiveQueryName(name) {
			continue
		}
		start, end := indices[8], indices[9]
		spans = append(spans, sensitiveSpan{start: start, end: end, priority: 0, label: "param", value: input[start:end]})
	}
	for _, indices := range labeledSecretPattern.FindAllStringSubmatchIndex(input, -1) {
		if isMarkerInterior(input, indices[0], indices[1]) {
			continue
		}
		start, end := indices[6], indices[7]
		if end-start >= 2 && ((input[start] == '"' && input[end-1] == '"') || (input[start] == '\'' && input[end-1] == '\'')) {
			start++
			end--
		}
		spans = append(spans, sensitiveSpan{
			start:    start,
			end:      end,
			priority: 1,
			label:    labelForSecretName(input[indices[2]:indices[3]]),
			value:    input[start:end],
		})
	}
	for _, indices := range bearerPattern.FindAllStringSubmatchIndex(input, -1) {
		start, end := indices[4], indices[5]
		spans = append(spans, sensitiveSpan{start: start, end: end, priority: 2, label: "token", value: input[start:end]})
	}
	sort.Slice(spans, func(left, right int) bool {
		if spans[left].start != spans[right].start {
			return spans[left].start < spans[right].start
		}
		if spans[left].priority != spans[right].priority {
			return spans[left].priority < spans[right].priority
		}
		return spans[left].end > spans[right].end
	})
	return spans
}

func isMarkerInterior(input string, start, end int) bool {
	return start > 0 && end < len(input) && input[start-1] == '<' && input[end] == '>' &&
		markerPattern.MatchString(input[start-1:end+1])
}

func (r *Redactor) redactPhones(input string) string {
	indices := phonePattern.FindAllStringIndex(input, -1)
	if len(indices) == 0 {
		return input
	}

	var out strings.Builder
	previous := 0
	for _, index := range indices {
		start, end := index[0], index[1]
		candidate := input[start:end]
		if !phoneBoundary(input, start, end) || !validPhoneDigits(candidate) {
			continue
		}
		out.WriteString(input[previous:start])
		out.WriteString(r.Marker("phone", candidate))
		previous = end
	}
	if previous == 0 {
		return input
	}
	out.WriteString(input[previous:])
	return out.String()
}

func phoneBoundary(input string, start, end int) bool {
	if (start != 0 && isASCIIWord(input[start-1])) || (end != len(input) && isASCIIWord(input[end])) {
		return false
	}
	if end < len(input) {
		tail := strings.ToLower(trimPhoneExtensionWhitespace(input[end:]))
		if strings.HasPrefix(tail, "ext ") || strings.HasPrefix(tail, "ext.") || strings.HasPrefix(tail, "extension ") ||
			(len(tail) > 1 && tail[0] == 'x' && tail[1] >= '0' && tail[1] <= '9') {
			return false
		}
	}
	return true
}

func trimPhoneExtensionWhitespace(input string) string {
	return strings.TrimLeft(input, " \t\r\n")
}

func validPhoneDigits(candidate string) bool {
	digits := 0
	first := byte(0)
	for idx := 0; idx < len(candidate); idx++ {
		if candidate[idx] >= '0' && candidate[idx] <= '9' {
			if first == 0 {
				first = candidate[idx]
			}
			digits++
		}
	}
	return digits >= 7 && digits <= 15 && first >= '1' && first <= '9'
}

func isASCIIWord(value byte) bool {
	return (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '_'
}

func labelForField(field string) string {
	words, ok := structuredFieldWords(field)
	if !ok {
		return ""
	}
	switch {
	case wordsEqual(words, "email"), wordsEqual(words, "email", "address"):
		return "email"
	case wordsEqual(words, "phone"), wordsEqual(words, "phone", "number"), wordsEqual(words, "mobile"), wordsEqual(words, "mobile", "phone"):
		return "phone"
	case wordsEndWith(words, "action", "token"):
		return "action_token"
	case wordsEndWith(words, "token"), wordsEqual(words, "authorization"), wordsEqual(words, "authorization", "header"), wordsEqual(words, "bearer"):
		return "token"
	case wordsEndWith(words, "password"), wordsEndWith(words, "passwd"), wordsEndWith(words, "pwd"), wordsEndWith(words, "secret"), wordsEndWith(words, "api", "key"), wordsEqual(words, "apikey"), wordsEqual(words, "key"):
		return "secret"
	default:
		return ""
	}
}

func structuredFieldWords(field string) ([]string, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return nil, false
	}
	words := make([]string, 0, 3)
	var word strings.Builder
	for idx := 0; idx < len(field); idx++ {
		value := field[idx]
		switch {
		case value >= 'A' && value <= 'Z':
			word.WriteByte(value + ('a' - 'A'))
		case (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9'):
			word.WriteByte(value)
		case value == ' ' || value == '_' || value == '-':
			if word.Len() == 0 {
				return nil, false
			}
			words = append(words, word.String())
			word.Reset()
		default:
			return nil, false
		}
	}
	if word.Len() == 0 {
		return nil, false
	}
	return append(words, word.String()), true
}

func wordsEqual(words []string, want ...string) bool {
	return len(words) == len(want) && wordsEndWith(words, want...)
}

func wordsEndWith(words []string, suffix ...string) bool {
	if len(words) < len(suffix) {
		return false
	}
	offset := len(words) - len(suffix)
	for idx := range suffix {
		if words[offset+idx] != suffix[idx] {
			return false
		}
	}
	return true
}

func isSensitiveQueryName(name string) bool {
	switch strings.ToLower(name) {
	case "token", "access_token", "refresh_token", "action_token", "secret", "password", "api_key", "key":
		return true
	default:
		return false
	}
}

func labelForSecretName(name string) string {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	if normalized == "action_token" {
		return "action_token"
	}
	if strings.Contains(normalized, "token") {
		return "token"
	}
	return "secret"
}

func normalizeLabel(label string) string {
	label = strings.ToLower(strings.TrimSpace(label))
	label = strings.ReplaceAll(label, "-", "_")
	if label == "" {
		return "secret"
	}
	return label
}
