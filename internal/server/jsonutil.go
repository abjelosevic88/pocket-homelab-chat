package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
)

// Reading the other apps' JSON without a struct per app.

func getJSONFrom(ctx context.Context, sib *siblingConn, path string, q url.Values) (any, error) {
	var raw json.RawMessage
	if err := sib.client.Do(ctx, http.MethodGet, path, q, nil, &raw); err != nil {
		return nil, err
	}
	var v any
	err := json.Unmarshal(raw, &v)
	return v, err
}

func postJSONTo(ctx context.Context, sib *siblingConn, path string, body any) (map[string]any, error) {
	var out map[string]any
	if err := sib.client.Do(ctx, http.MethodPost, path, nil, body, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

// rows finds the list in an answer: bare, or under one of keys.
func rows(v any, keys ...string) []map[string]any {
	var arr []any
	switch t := v.(type) {
	case []any:
		arr = t
	case map[string]any:
		for _, k := range append(keys, "items", "results") {
			if a, ok := t[k].([]any); ok {
				arr = a
				break
			}
		}
	}
	out := []map[string]any{}
	for _, x := range arr {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case []any:
			var parts []string
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", ")
			}
		}
	}
	return ""
}

func num(m map[string]any, k string) float64 {
	switch v := m[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	}
	return 0
}

func obj(m map[string]any, k string) map[string]any {
	if o, ok := m[k].(map[string]any); ok {
		return o
	}
	return map[string]any{}
}

// day10 is the YYYY-MM-DD part of a date, a timestamp or unix seconds.
func day10(v any) string {
	switch t := v.(type) {
	case string:
		if len(t) >= 10 {
			return t[:10]
		}
	case float64:
		if t > 1e9 {
			return time.Unix(int64(t), 0).Format("2006-01-02")
		}
	}
	return ""
}

func idOf(m map[string]any) string {
	switch v := m["id"].(type) {
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	}
	return ""
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	return strings.Join(out, sep)
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

func chiParam(r *http.Request, name string) string { return chi.URLParam(r, name) }
