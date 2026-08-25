package query

import (
	"fmt"
	"strings"
)

type Request struct {
	Operation  string
	Positional string
	Params     map[string]string
	Fields     []string
	Raw        string
}

func ParseBatch(input string) ([]Request, error) {
	parts, err := splitTopLevel(input, ';')
	if err != nil {
		return nil, err
	}

	requests := make([]Request, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		request, err := parseSingle(part)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}

	if len(requests) == 0 {
		return nil, fmt.Errorf("query is empty")
	}
	return requests, nil
}

func parseSingle(input string) (Request, error) {
	operationPart, fieldsPart, err := splitFieldsBlock(input)
	if err != nil {
		return Request{}, err
	}

	openIdx := strings.IndexByte(operationPart, '(')
	closeIdx := strings.LastIndexByte(operationPart, ')')
	if openIdx <= 0 || closeIdx < openIdx {
		return Request{}, fmt.Errorf("invalid query %q", input)
	}

	name := strings.TrimSpace(operationPart[:openIdx])
	if name == "" {
		return Request{}, fmt.Errorf("missing operation name in %q", input)
	}

	paramsContent := strings.TrimSpace(operationPart[openIdx+1 : closeIdx])
	params, positional, err := parseParams(paramsContent)
	if err != nil {
		return Request{}, err
	}

	fields, err := parseFields(fieldsPart)
	if err != nil {
		return Request{}, err
	}

	return Request{
		Operation:  name,
		Positional: positional,
		Params:     params,
		Fields:     fields,
		Raw:        input,
	}, nil
}

func splitFieldsBlock(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("query is empty")
	}

	quote := rune(0)
	parenDepth := 0
	fieldsStart := -1
	fieldsEnd := -1

	for idx, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '(':
			parenDepth++
		case r == ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case r == '{' && parenDepth == 0:
			fieldsStart = idx
		case r == '}' && parenDepth == 0:
			fieldsEnd = idx
		}
	}

	switch {
	case fieldsStart == -1 && fieldsEnd == -1:
		return input, "", nil
	case fieldsStart == -1 || fieldsEnd == -1 || fieldsEnd < fieldsStart:
		return "", "", fmt.Errorf("invalid fields block in %q", input)
	}

	operationPart := strings.TrimSpace(input[:fieldsStart])
	fieldsPart := strings.TrimSpace(input[fieldsStart+1 : fieldsEnd])
	trailing := strings.TrimSpace(input[fieldsEnd+1:])
	if trailing != "" {
		return "", "", fmt.Errorf("unexpected trailing text %q", trailing)
	}

	return operationPart, fieldsPart, nil
}

func parseParams(input string) (map[string]string, string, error) {
	params := map[string]string{}
	input = strings.TrimSpace(input)
	if input == "" {
		return params, "", nil
	}

	parts, err := splitTopLevel(input, ',')
	if err != nil {
		return nil, "", err
	}

	var positional string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		if !strings.Contains(part, "=") {
			if positional != "" {
				return nil, "", fmt.Errorf("multiple positional arguments are not supported")
			}
			positional = unquote(part)
			continue
		}

		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, "", fmt.Errorf("invalid parameter %q", part)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, "", fmt.Errorf("parameter name is empty")
		}
		if _, exists := params[key]; exists {
			return nil, "", fmt.Errorf("duplicate parameter %q", key)
		}
		params[key] = unquote(strings.TrimSpace(value))
	}

	return params, positional, nil
}

func parseFields(input string) ([]string, error) {
	input = strings.TrimSpace(strings.ReplaceAll(input, ",", " "))
	if input == "" {
		return nil, nil
	}
	return strings.Fields(input), nil
}

func splitTopLevel(input string, separator rune) ([]string, error) {
	var parts []string
	var builder strings.Builder
	quote := rune(0)
	parenDepth := 0
	braceDepth := 0

	for _, r := range input {
		switch {
		case quote != 0:
			builder.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		case r == '"' || r == '\'':
			quote = r
			builder.WriteRune(r)
			continue
		case r == '(':
			parenDepth++
		case r == ')':
			parenDepth--
		case r == '{':
			braceDepth++
		case r == '}':
			braceDepth--
		case r == separator && parenDepth == 0 && braceDepth == 0:
			parts = append(parts, builder.String())
			builder.Reset()
			continue
		}

		if parenDepth < 0 || braceDepth < 0 {
			return nil, fmt.Errorf("unbalanced query expression")
		}
		builder.WriteRune(r)
	}

	if quote != 0 || parenDepth != 0 || braceDepth != 0 {
		return nil, fmt.Errorf("unbalanced query expression")
	}

	parts = append(parts, builder.String())
	return parts, nil
}

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
