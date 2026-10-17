package interfaces

import (
	"embed"
	"io/fs"
)

// The dashboard is a React app (web/, built by `npm run build`) whose output
// lands in webui/dist and is embedded here, so the binary serves it with no
// external requests — it fetches only the same server's /api/* JSON.

//go:embed all:webui/dist
var appFS embed.FS

// appDist is webui/dist as the root (index.html, assets/).
var appDist, _ = fs.Sub(appFS, "webui/dist")
