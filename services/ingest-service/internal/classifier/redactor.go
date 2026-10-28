package classifier

import (
	"fmt"
	"hash/fnv"
	"regexp"
)

var (
	jwtRe       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)
	authParamRe = regexp.MustCompile(`(?i)(auth-token|access_token|token|bearer)(%20|=|\s+)([A-Za-z0-9._~+/-]{20,})`)
)

func shortHash8(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%08x", uint32(h.Sum64()))
}

// RedactSecrets заменяет JWT и Bearer/auth-token на <REDACTED_JWT:hash8> / <REDACTED_TOKEN:hash8>
func RedactSecrets(input string) string {
	if input == "" {
		return ""
	}

	// 1. Redact JWT tokens
	res := jwtRe.ReplaceAllStringFunc(input, func(m string) string {
		return "<REDACTED_JWT:" + shortHash8(m) + ">"
	})

	// 2. Redact other auth tokens / Bearer
	res = authParamRe.ReplaceAllStringFunc(res, func(m string) string {
		sub := authParamRe.FindStringSubmatch(m)
		if len(sub) == 4 {
			return sub[1] + sub[2] + "<REDACTED_TOKEN:" + shortHash8(sub[3]) + ">"
		}
		return m
	})

	return res
}
