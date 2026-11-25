package strain

import "testing"

// The errors met so far (her log, 2026-11-22..25) read as their strains.
func TestKind(t *testing.T) {
	for err, want := range map[string]string{
		"Error 503, Message: This model is currently experiencing high demand. Please try again later.": "busy",
		"Request aborted": "busy",
		"Error 429, Message: You exceeded your current quota, please check your plan and billing details.": "tired",
		"RESOURCE_EXHAUSTED": "tired",
		`Post "https://generativelanguage.googleapis.com/v1beta/models": dial tcp: lookup generativelanguage.googleapis.com: no such host`: "offline",
		"websocket: close 1008 (policy violation): The operation was aborted.":                                                             "",
		"Function call is missing a thought_signature in functionCall parts.":                                                              "",
		"mapper: no genre given": "",
	} {
		if got := Kind(err); got != want {
			t.Errorf("%q: %q, want %q", err, got, want)
		}
	}
}
