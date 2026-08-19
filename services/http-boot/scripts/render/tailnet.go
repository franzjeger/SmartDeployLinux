package main

import (
	"bytes"
	"net/http"
	"os"
)

func clientIsTailnet(r *http.Request) bool {
	t := os.Getenv("DEPLOY_FQDN_TAILNET")
	return t != "" && r.Header.Get("X-Forwarded-Host") == t
}

// rewriteForClient rewrites LAN deploy URLs to their tailnet equivalents
// when the request comes from a tailnet client, so an off-LAN machine that
// reached us over tailscale gets URLs it can actually fetch. LAN clients
// are left untouched. Enabled by DEPLOY_FQDN_TAILNET (HTTPS, :443 passthrough)
// and DEPLOY_FQDN_TAILNET_HTTP (HTTP, :8080 passthrough).
func rewriteForClient(r *http.Request, body []byte) []byte {
	if !clientIsTailnet(r) {
		return body
	}
	lan := os.Getenv("DEPLOY_FQDN")
	tHTTPS := os.Getenv("DEPLOY_FQDN_TAILNET")
	tHTTP := os.Getenv("DEPLOY_FQDN_TAILNET_HTTP")
	if lan == "" || tHTTPS == "" {
		return body
	}
	if tHTTP != "" {
		body = bytes.ReplaceAll(body, []byte("http://"+lan+":8080"), []byte("http://"+tHTTP))
	}
	body = bytes.ReplaceAll(body, []byte("https://"+lan), []byte("https://"+tHTTPS))
	return body
}
