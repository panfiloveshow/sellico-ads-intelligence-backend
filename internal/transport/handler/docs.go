package handler

import (
	"html/template"
	"net/http"

	"github.com/panfiloveshow/sellico-ads-intelligence-backend/internal/transport/dto"
)

// DocsHandler serves the baseline OpenAPI spec and a lightweight docs landing page.
type DocsHandler struct {
	spec []byte
}

// NewDocsHandler creates a docs handler backed by an embedded OpenAPI file.
func NewDocsHandler(spec []byte) *DocsHandler {
	return &DocsHandler{spec: spec}
}

// Spec serves the raw OpenAPI YAML file.
func (h *DocsHandler) Spec(w http.ResponseWriter, _ *http.Request) {
	if len(h.spec) == 0 {
		dto.WriteError(w, http.StatusInternalServerError, "DOCS_UNAVAILABLE", "openapi spec is unavailable")
		return
	}

	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(h.spec)
}

// Index serves a simple HTML docs landing page without third-party assets.
func (h *DocsHandler) Index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	_ = docsPageTmpl.Execute(w, struct {
		SpecURL string
	}{
		SpecURL: "/openapi.yaml",
	})
}

var docsPageTmpl = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sellico Ads Intelligence API Docs</title>
  <style>
    :root {
      color-scheme: light;
      --bg: #f6f4ee;
      --panel: #fffdf8;
      --text: #1d1d1b;
      --muted: #5e5b53;
      --accent: #0f766e;
      --border: #d9d2c2;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: "Iowan Old Style", "Palatino Linotype", "Book Antiqua", serif;
      background: linear-gradient(180deg, #efe8d9 0%, var(--bg) 100%);
      color: var(--text);
    }
    main {
      max-width: 860px;
      margin: 0 auto;
      padding: 48px 20px 64px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 18px;
      padding: 28px;
      box-shadow: 0 18px 50px rgba(29, 29, 27, 0.08);
    }
    h1 {
      margin: 0 0 12px;
      font-size: clamp(2rem, 4vw, 3.4rem);
      line-height: 0.95;
      letter-spacing: -0.04em;
    }
    p {
      margin: 0 0 16px;
      font-size: 1.05rem;
      line-height: 1.6;
      color: var(--muted);
    }
    .actions {
      display: flex;
      gap: 12px;
      flex-wrap: wrap;
      margin: 26px 0 22px;
    }
    .button, .link {
      display: inline-block;
      text-decoration: none;
      border-radius: 999px;
      padding: 12px 18px;
      font-size: 0.98rem;
    }
    .button {
      background: var(--accent);
      color: #fff;
    }
    .link {
      color: var(--text);
      border: 1px solid var(--border);
      background: transparent;
    }
    code {
      font-family: "SFMono-Regular", Menlo, Monaco, Consolas, monospace;
      background: #f2ede1;
      padding: 2px 6px;
      border-radius: 6px;
      font-size: 0.92em;
      color: #21403d;
    }
    ul {
      margin: 18px 0 0;
      padding-left: 18px;
      color: var(--muted);
    }
    li { margin: 10px 0; line-height: 1.5; }
  </style>
</head>
<body>
  <main>
    <section class="panel">
      <h1>Sellico Ads Intelligence API</h1>
      <p>
        Runtime documentation entrypoint for the backend contract. The canonical
        OpenAPI file is served by this backend and can be consumed by local tooling,
        API clients, or external documentation generators.
      </p>
      <div class="actions">
        <a class="button" href="{{ .SpecURL }}">Open OpenAPI YAML</a>
        <a class="link" href="https://editor.swagger.io/?url={{ .SpecURL }}">Try in Swagger Editor</a>
      </div>
      <ul>
        <li>Canonical spec URL: <code>{{ .SpecURL }}</code></li>
        <li>Tenant-scoped endpoints require the <code>X-Workspace-ID</code> header.</li>
        <li>Compatibility aliases are implemented by the router but intentionally excluded from the primary contract.</li>
      </ul>
    </section>
  </main>
</body>
</html>`))
