package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func validateInputSchema(schema map[string]any, raw json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid tool input JSON: %w", err)
	}
	return validateSchemaValue(schema, value, "")
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	for _, schemaType := range schemaTypes(schema["type"]) {
		if schemaType == "null" && value == nil {
			return nil
		}
		if schemaType != "null" && typeMatches(schemaType, value) {
			return validateTypedSchema(schema, value, path, schemaType)
		}
	}
	if len(schemaTypes(schema["type"])) > 0 {
		return fmt.Errorf("%s must be %s", displayPath(path), strings.Join(schemaTypes(schema["type"]), " or "))
	}
	return validateTypedSchema(schema, value, path, "")
}

func validateTypedSchema(schema map[string]any, value any, path, schemaType string) error {
	if enumValues, ok := schema["enum"].([]any); ok && len(enumValues) > 0 {
		if !containsEnumValue(enumValues, value) {
			return fmt.Errorf("%s must be one of the allowed values", displayPath(path))
		}
	}
	if schemaType == "object" || (schemaType == "" && isObject(value)) {
		object, _ := value.(map[string]any)
		if err := validateRequired(schema, object, path); err != nil {
			return err
		}
		properties, hasProperties := mapValue(schema["properties"])
		if hasProperties {
			for name, propertySchemaAny := range properties {
				propertySchema, ok := mapValue(propertySchemaAny)
				if !ok {
					continue
				}
				propertyValue, exists := object[name]
				if !exists {
					continue
				}
				if err := validateSchemaValue(propertySchema, propertyValue, joinPath(path, name)); err != nil {
					return err
				}
			}
		}
		if additional, ok := schema["additionalProperties"].(bool); ok && !additional && hasProperties {
			for name := range object {
				if _, allowed := properties[name]; !allowed {
					return fmt.Errorf("unexpected field: %s", joinPath(path, name))
				}
			}
		}
	}
	if schemaType == "string" {
		text, _ := value.(string)
		if minimum, ok := intValue(schema["minLength"]); ok && len(text) < minimum {
			return fmt.Errorf("%s must have length at least %d", displayPath(path), minimum)
		}
		if maximum, ok := intValue(schema["maxLength"]); ok && len(text) > maximum {
			return fmt.Errorf("%s must have length at most %d", displayPath(path), maximum)
		}
		if pattern, ok := schema["pattern"].(string); ok && pattern != "" {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil {
				return fmt.Errorf("%s has invalid pattern constraint: %w", displayPath(path), err)
			}
			if !matched {
				return fmt.Errorf("%s must match pattern %s", displayPath(path), pattern)
			}
		}
	}
	if schemaType == "number" || schemaType == "integer" {
		number, ok := numberValue(value)
		if ok {
			if minimum, ok := numberConstraint(schema["minimum"]); ok && number < minimum {
				return fmt.Errorf("%s must be at least %s", displayPath(path), formatNumber(minimum))
			}
			if maximum, ok := numberConstraint(schema["maximum"]); ok && number > maximum {
				return fmt.Errorf("%s must be at most %s", displayPath(path), formatNumber(maximum))
			}
		}
	}
	if schemaType == "array" || (schemaType == "" && isArray(value)) {
		array, _ := value.([]any)
		if minimum, ok := intValue(schema["minItems"]); ok && len(array) < minimum {
			return fmt.Errorf("%s must contain at least %d item(s)", displayPath(path), minimum)
		}
		if maximum, ok := intValue(schema["maxItems"]); ok && len(array) > maximum {
			return fmt.Errorf("%s must contain at most %d item(s)", displayPath(path), maximum)
		}
		itemSchema, ok := mapValue(schema["items"])
		if !ok {
			return nil
		}
		for index, item := range array {
			if err := validateSchemaValue(itemSchema, item, fmt.Sprintf("%s[%d]", displayPath(path), index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	default:
		return 0, false
	}
}

func numberConstraint(value any) (float64, bool) {
	return numberValue(value)
}

func formatNumber(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func validateRequired(schema map[string]any, object map[string]any, path string) error {
	for _, field := range stringSlice(schema["required"]) {
		if _, ok := object[field]; !ok {
			if path == "" {
				return fmt.Errorf("missing required field: %s", field)
			}
			return fmt.Errorf("missing required field: %s.%s", path, field)
		}
	}
	return nil
}

func schemaTypes(value any) []string {
	switch typed := value.(type) {
	case string:
		if typed == "" {
			return nil
		}
		return []string{typed}
	case []any:
		out := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func typeMatches(schemaType string, value any) bool {
	switch schemaType {
	case "object":
		return isObject(value)
	case "array":
		return isArray(value)
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		return isNumber(value)
	case "integer":
		return isInteger(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return true
	}
}

func isObject(value any) bool {
	_, ok := value.(map[string]any)
	return ok
}

func isArray(value any) bool {
	_, ok := value.([]any)
	return ok
}

func isNumber(value any) bool {
	switch value.(type) {
	case json.Number, float64, float32, int, int64, int32:
		return true
	default:
		return false
	}
}

func isInteger(value any) bool {
	switch typed := value.(type) {
	case json.Number:
		_, err := typed.Int64()
		return err == nil
	case int, int64, int32:
		return true
	default:
		return false
	}
}

func mapValue(value any) (map[string]any, bool) {
	typed, ok := value.(map[string]any)
	return typed, ok
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func containsEnumValue(enumValues []any, value any) bool {
	for _, candidate := range enumValues {
		if fmt.Sprint(candidate) == fmt.Sprint(value) {
			return true
		}
	}
	return false
}

func joinPath(prefix, field string) string {
	if prefix == "" {
		return field
	}
	return prefix + "." + field
}

func displayPath(path string) string {
	if path == "" {
		return "input"
	}
	return path
}
