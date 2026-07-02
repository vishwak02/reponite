// Package interfaces is the delivery layer: the CLI wiring, the MCP server
// (stdio | HTTP), the REST API, the webhook receiver, auth, and response
// formatting (architecture §14, §15).
//
// Planned files: mcp.go rest.go webhooksrv.go auth.go output.go. The
// Authenticator seam (auth.go) abstracts token/mTLS today and OIDC later
// (ADR-012).
package interfaces
