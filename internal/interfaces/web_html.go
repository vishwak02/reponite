package interfaces

import _ "embed"

// The read-only dashboard's assets live as real files under webui/ and are
// embedded at build time (//go:embed). The dashboard is vanilla HTML/CSS/JS
// with no external requests — it fetches only the same server's /api/* JSON —
// so the three files are served directly (see web.go Routes).

//go:embed webui/index.html
var dashboardHTML string

//go:embed webui/style.css
var dashboardCSS string

//go:embed webui/app.js
var dashboardJS string
