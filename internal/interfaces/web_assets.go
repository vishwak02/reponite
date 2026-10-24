// web_assets.go serves the dashboard's built assets. The build writes a
// brotli (.br) and a gzip (.gz) copy beside every compressible file, so the
// answer is the smallest variant the browser accepts, chosen per request and
// compressed once at build time rather than on every response. Asset names
// carry a content hash, so they are cacheable for good.
package interfaces

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// assetEncodings are the precompressed variants, best first.
var assetEncodings = []struct{ name, ext string }{{"br", ".br"}, {"gzip", ".gz"}}

// assetHandler serves files of dist (the app's assets/ directory and below),
// picking a precompressed variant when the request's Accept-Encoding allows.
func assetHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// only files under assets/, however the path was spelled
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		raw, err := fs.ReadFile(dist, name)
		if err != nil || !strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		ctype := mime.TypeByExtension(path.Ext(name))
		if ctype == "" {
			ctype = http.DetectContentType(raw)
		}
		h.Set("Content-Type", ctype)
		body := raw
		hasVariant := false
		for _, enc := range assetEncodings {
			b, err := fs.ReadFile(dist, name+enc.ext)
			if err != nil {
				continue
			}
			hasVariant = true
			if acceptsEncoding(r.Header.Get("Accept-Encoding"), enc.name) {
				body = b
				h.Set("Content-Encoding", enc.name)
				break
			}
		}
		if hasVariant {
			h.Add("Vary", "Accept-Encoding")
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			w.Write(body)
		}
	})
}

// acceptsEncoding reports whether an Accept-Encoding header admits coding
// (RFC 9110 §12.5.3): listed, or covered by "*", with a non-zero q.
func acceptsEncoding(header, coding string) bool {
	star := false
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		switch name {
		case coding:
			return q > 0
		case "*":
			star = q > 0
		}
	}
	return star
}
