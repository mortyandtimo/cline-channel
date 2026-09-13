package main

import (
	"bytes"
	"crypto/subtle"
	"embed"
	"html/template"
	"net/http"

	"cline-channel/sdk/pluginapi"
)

//go:embed web/*
var panelFiles embed.FS

var panelAssetNames = []string{"panel.css", "panel.js", "panel-api.js", "panel-editor.js"}
var panelSessionToken = newSessionID()

func panelTokenMatches(token string) bool {
	return subtle.ConstantTimeCompare([]byte(token), []byte(panelSessionToken)) == 1
}

func renderPanelHTML() string {
	var output bytes.Buffer
	tmpl := template.Must(template.ParseFS(panelFiles, "web/panel.html"))
	if err := tmpl.Execute(&output, map[string]string{"Version": pluginVersion, "Token": panelSessionToken}); err != nil {
		return "Cline panel could not be rendered"
	}
	return output.String()
}

func panelAsset(name string) ([]byte, string, bool) {
	for _, asset := range panelAssetNames {
		if name == asset {
			content, err := panelFiles.ReadFile("web/" + asset)
			if err != nil {
				return nil, "", false
			}
			mime := "text/javascript; charset=utf-8"
			if asset == "panel.css" {
				mime = "text/css; charset=utf-8"
			}
			return content, mime, true
		}
	}
	return nil, "", false
}

func panelResponse(mime string, content []byte) ([]byte, error) {
	return okEnvelope(pluginapi.ManagementResponse{
		StatusCode: http.StatusOK, Body: content,
		Headers: http.Header{
			"Content-Type": {mime}, "Cache-Control": {"no-store"},
			"X-Content-Type-Options": {"nosniff"}, "Referrer-Policy": {"same-origin"},
		},
	})
}
