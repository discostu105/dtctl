package template

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// ParseSetFlags parses --set flags into a map
// Format: key=value
func ParseSetFlags(setFlags []string) (map[string]interface{}, error) {
	vars := make(map[string]interface{})

	for _, flag := range setFlags {
		parts := strings.SplitN(flag, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --set format: %q (expected key=value)", flag)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return nil, fmt.Errorf("empty key in --set flag: %q", flag)
		}

		vars[key] = value
	}

	return vars, nil
}

// funcMap returns the template functions shared by rendering and validation.
func funcMap() template.FuncMap {
	return template.FuncMap{
		"default": func(defaultVal interface{}, value ...interface{}) interface{} {
			// If no value provided or value is empty/zero, return default
			if len(value) == 0 {
				return defaultVal
			}
			v := value[0]
			if v == nil || v == "" {
				return defaultVal
			}
			return v
		},
		"dqlString": DQLString,
	}
}

// DQLString renders a value as a properly escaped, quoted DQL string literal.
// Recipe templates interpolate string-typed params exclusively through this
// func (`== {{.ns | dqlString}}`, never `== "{{.ns}}"`) so a value can never
// break out of the literal (RECIPES_CONCEPT.md §2.2).
func DQLString(v interface{}) string {
	s := fmt.Sprintf("%v", v)
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// RenderTemplate renders a template string with the provided variables
// Uses Go's text/template syntax with support for default values
func RenderTemplate(templateStr string, vars map[string]interface{}) (string, error) {
	// Parse the template with missingkey=zero (so variables evaluate to zero value)
	tmpl, err := template.New("query").Funcs(funcMap()).Option("missingkey=zero").Parse(templateStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	// Execute the template
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// ContainsTemplate checks if a string contains template syntax
func ContainsTemplate(str string) bool {
	return strings.Contains(str, "{{") && strings.Contains(str, "}}")
}

// ValidateTemplate checks if a template is valid and returns required variables
// This is a best-effort function that may not catch all cases
func ValidateTemplate(templateStr string) ([]string, error) {
	// Try to parse the template to validate syntax
	_, err := template.New("validate").Funcs(funcMap()).Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template syntax: %w", err)
	}

	// Extract variable names from template
	// Note: This is a simplified approach - Go's template package doesn't
	// provide a built-in way to extract all variable names
	var vars []string
	seen := make(map[string]bool)

	// Simple regex-like extraction of {{.VarName}} patterns
	inTemplate := false
	var currentVar strings.Builder

	for i := 0; i < len(templateStr); i++ {
		if i < len(templateStr)-1 && templateStr[i] == '{' && templateStr[i+1] == '{' {
			inTemplate = true
			currentVar.Reset()
			i++ // Skip next {
			continue
		}

		if inTemplate && i < len(templateStr)-1 && templateStr[i] == '}' && templateStr[i+1] == '}' {
			inTemplate = false
			varName := strings.TrimSpace(currentVar.String())

			// Extract variable name (format: {{.VarName}} or {{.VarName | default "value"}})
			if strings.HasPrefix(varName, ".") {
				varName = strings.TrimPrefix(varName, ".")
				// Remove any function calls like "| default"
				if idx := strings.Index(varName, " "); idx != -1 {
					varName = varName[:idx]
				}
				if idx := strings.Index(varName, "|"); idx != -1 {
					varName = strings.TrimSpace(varName[:idx])
				}

				if varName != "" && !seen[varName] {
					vars = append(vars, varName)
					seen[varName] = true
				}
			}

			i++ // Skip next }
			continue
		}

		if inTemplate {
			currentVar.WriteByte(templateStr[i])
		}
	}

	return vars, nil
}
