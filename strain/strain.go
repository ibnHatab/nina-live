// Package strain reads an API error as what it says about Nina herself (the user, 2026-11-25:
// not to ignore it as a word signal: a proposition linked to Nina, on the quantized port, that
// the subdomains interpret later). The kinds are the semantic world's (home.Strains):
//
//	busy     the service is in high demand (503, unavailable, overloaded, a request timed out)
//	tired    a quota is spent (429, resource exhausted, rate limited, the plan's limit)
//	offline  the service cannot be reached (no such host, connection refused, network down)
//
// Anything else is not a strain: a bug, a bad request ("" — the error stays the caller's). A
// websocket the server closed as "aborted" is its routine end of a quiet session, not a strain.
package strain

import "strings"

var kinds = []struct {
	kind string
	says []string
}{
	{"offline", []string{"no such host", "connection refused", "network is unreachable", "no route to host",
		"dial tcp", "dial udp", "temporary failure in name resolution"}},
	{"tired", []string{"429", "resource_exhausted", "resource exhausted", "quota", "rate limit", "rate-limit",
		"too many requests", "check your plan", "billing"}},
	{"busy", []string{"503", "unavailable", "overloaded", "high demand", "try again later", "502", "504",
		"deadline exceeded", "deadline_exceeded", "timed out", "timeout", "request aborted", "internal error", "500 "}},
}

// Kind is the strain an error says, "" when it says none.
func Kind(err string) string {
	e := strings.ToLower(err)
	for _, k := range kinds {
		for _, s := range k.says {
			if strings.Contains(e, s) {
				return k.kind
			}
		}
	}
	return ""
}

// Gist is an error's first line, short (the strain's ref in the log).
func Gist(err string) string {
	if i := strings.IndexByte(err, '\n'); i >= 0 {
		err = err[:i]
	}
	if len(err) > 120 {
		err = err[:120] + "…"
	}
	return strings.TrimSpace(err)
}
