package main

import "net/http"

func upstreamErrorEnvelope(status int, raw []byte) []byte {
	message := extractErrorMessage(raw)
	if message == "" {
		message = "Cline upstream returned HTTP " + itoa(status)
	}
	return mustJSON(map[string]any{
		"ok": false,
		"error": map[string]any{
			"code": "cline_upstream_error", "message": message,
			"retryable":   status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500,
			"http_status": status,
		},
	})
}
