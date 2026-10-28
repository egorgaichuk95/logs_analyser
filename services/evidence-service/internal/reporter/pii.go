package reporter

import (
	"fmt"
	"hash/fnv"
	"net"
	"regexp"
	"strconv"
	"strings"
)

var (
	emailRe       = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	displayNameRe = regexp.MustCompile(`(?i)(display_name)(%22%3A%22|":\s*"|=)([^"&%\s]+)`)
	ipv4Re        = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)
	jwtFallbackRe = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

func hash4(s string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%04x", h.Sum32()&0xffff)
}

// isPrivateOrLocalIP проверяет, принадлежит ли IP к приватным (10.x, 172.16-31.x, 192.168.x) или loopback (127.x)
func isPrivateOrLocalIP(ipStr string) bool {
	parsed := net.ParseIP(ipStr)
	if parsed == nil {
		return false
	}
	ip4 := parsed.To4()
	if ip4 == nil {
		return parsed.IsLoopback() || parsed.IsPrivate()
	}

	// 10.0.0.0/8
	if ip4[0] == 10 {
		return true
	}
	// 172.16.0.0/12 (172.16.0.0 - 172.31.255.255)
	if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
		return true
	}
	// 192.168.0.0/16
	if ip4[0] == 192 && ip4[1] == 168 {
		return true
	}
	// 127.0.0.0/8
	if ip4[0] == 127 {
		return true
	}

	return false
}

// RedactPII маскирует email, display_name, внешние IP (сохраняя приватные 10.x, 172.16-31.x, 192.168.x, 127.x) и JWT
func RedactPII(text string) string {
	if text == "" {
		return ""
	}

	// 1. JWT fallback tokens
	out := jwtFallbackRe.ReplaceAllStringFunc(text, func(m string) string {
		return "<TOKEN_" + hash4(m) + ">"
	})

	// 2. Display names (key delimiter value)
	out = displayNameRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := displayNameRe.FindStringSubmatch(m)
		if len(sub) == 4 {
			return sub[1] + sub[2] + "<USER_" + hash4(sub[3]) + ">"
		}
		return m
	})

	// 3. Email addresses
	out = emailRe.ReplaceAllStringFunc(out, func(m string) string {
		return "<EMAIL_" + hash4(m) + ">"
	})

	// 4. IPv4 addresses
	out = ipv4Re.ReplaceAllStringFunc(out, func(m string) string {
		parts := strings.Split(m, ".")
		if len(parts) != 4 {
			return m
		}
		for _, p := range parts {
			val, err := strconv.Atoi(p)
			if err != nil || val < 0 || val > 255 {
				return m
			}
		}

		if isPrivateOrLocalIP(m) {
			return m
		}
		return "<CLIENT_IP_" + hash4(m) + ">"
	})

	return out
}
