package server

import (
	"encoding/json"
	"html"
	"net/url"
	"strconv"
	"strings"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func remarshal(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// cleanURL keeps http(s) links only ("" stays "").
func cleanURL(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false
	}
	return v, true
}

func cleanCurrency(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if len(v) != 3 {
		return ""
	}
	for _, c := range v {
		if c < 'A' || c > 'Z' {
			return ""
		}
	}
	return v
}

func trimP(dst *string, v *string) {
	if v != nil {
		*dst = strings.TrimSpace(*v)
	}
}

func i64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

func htmlUnescape(s string) string { return html.UnescapeString(s) }

func pathUnescape(s string) (string, error) { return url.PathUnescape(s) }
