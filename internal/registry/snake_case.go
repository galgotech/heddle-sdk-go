package registry

import "strings"

func toSnakeCase(str string) string {
	var result strings.Builder

	for i, r := range str {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := str[i-1]
			nextIsLower := false

			if i+1 < len(str) {
				next := str[i+1]
				if next >= 'a' && next <= 'z' {
					nextIsLower = true
				}
			}

			prevIsLowerOrDigit := (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9')
			prevIsUpper := prev >= 'A' && prev <= 'Z'

			if prevIsLowerOrDigit || (prevIsUpper && nextIsLower) {
				result.WriteByte('_')
			}
		}

		result.WriteRune(r)
	}

	return strings.ToLower(result.String())
}
