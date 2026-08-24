package query

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func JSONValue(results []Result) any {
	if len(results) == 1 {
		return jsonValue(results[0])
	}

	values := make([]any, 0, len(results))
	for _, result := range results {
		values = append(values, jsonValue(result))
	}
	return values
}

func RenderCompact(results []Result) (string, error) {
	if len(results) == 0 {
		return "", nil
	}

	sections := make([]string, 0, len(results))
	for idx, result := range results {
		section, err := renderCompactResult(result)
		if err != nil {
			return "", err
		}
		if len(results) > 1 {
			section = fmt.Sprintf("query:%d operation:%s\n%s", idx+1, result.Operation, section)
		}
		sections = append(sections, strings.TrimRight(section, "\n"))
	}

	return strings.Join(sections, "\n\n"), nil
}

func jsonValue(result Result) any {
	switch result.Kind {
	case ResultKindObject:
		return result.Object
	case ResultKindList:
		if result.Page != nil {
			return map[string]any{
				"items": result.Items,
				"page":  result.Page,
			}
		}
		return result.Items
	default:
		return nil
	}
}

func renderCompactResult(result Result) (string, error) {
	switch result.Kind {
	case ResultKindObject:
		return renderCompactObject(result.Object, result.Columns), nil
	case ResultKindList:
		return renderCompactList(result.Items, result.Columns, result.Page), nil
	default:
		return "", fmt.Errorf("unsupported result kind %q", result.Kind)
	}
}

func renderCompactObject(object map[string]any, columns []string) string {
	if len(columns) == 0 {
		columns = sortedObjectKeys(object)
	}

	lines := make([]string, 0, len(columns))
	for _, column := range columns {
		lines = append(lines, fmt.Sprintf("%s:%s", column, compactValue(object[column])))
	}
	return strings.Join(lines, "\n")
}

func renderCompactList(items []map[string]any, columns []string, page map[string]any) string {
	var builder strings.Builder
	if len(columns) == 0 && len(items) > 0 {
		columns = sortedObjectKeys(items[0])
	}

	if len(columns) > 0 {
		builder.WriteString(strings.Join(columns, ","))
		builder.WriteByte('\n')
	}

	for _, item := range items {
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			values = append(values, csvValue(compactValue(item[column])))
		}
		builder.WriteString(strings.Join(values, ","))
		builder.WriteByte('\n')
	}

	if page != nil {
		if line := compactPage(page); line != "" {
			builder.WriteByte('\n')
			builder.WriteString(line)
		}
	}

	return strings.TrimRight(builder.String(), "\n")
}

func compactPage(page map[string]any) string {
	if len(page) == 0 {
		return ""
	}

	keys := sortedObjectKeys(page)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+compactValue(page[key]))
	}
	return "page:" + strings.Join(parts, " ")
}

func compactValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.ReplaceAll(typed, "\n", "\\n")
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	case int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
		return fmt.Sprintf("%v", typed)
	default:
		var buffer bytes.Buffer
		encoder := json.NewEncoder(&buffer)
		encoder.SetEscapeHTML(false)
		err := encoder.Encode(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return strings.TrimSuffix(buffer.String(), "\n")
	}
}

func csvValue(value string) string {
	if value == "" {
		return ""
	}
	if !strings.ContainsAny(value, ",\"\n") {
		return value
	}

	var buffer bytes.Buffer
	buffer.WriteByte('"')
	for _, r := range value {
		if r == '"' {
			buffer.WriteString(`""`)
			continue
		}
		buffer.WriteRune(r)
	}
	buffer.WriteByte('"')
	return buffer.String()
}
