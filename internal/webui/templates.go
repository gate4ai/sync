package webui

import _ "embed"

// logoSVG is the gate4.ai logo, the same file the web app serves as
// /favicon.svg — the header and the tab icon here use it so the settings
// page reads as part of gate4.ai rather than a separate tool.
//
//go:embed logo.svg
var logoSVG []byte

// templates are plain Go html/template strings, embedded rather than read
// from disk — this is a single-binary CLI tool, not a web app with a
// deploy step that could lose a sibling assets/ directory.
var templates = map[string]string{
	"home":   homeHTML + layoutHTML + foldersListHTML,
	"browse": browseHTML + layoutHTML + foldersListHTML,
	"error":  errorHTML + layoutHTML + foldersListHTML,
}

// layoutHTML is the page chrome shared by every page: the stylesheet and
// the header. Colors, radii and type follow the gate4.ai web app
// (server/web/src/index.css and its shadcn components), so moving between
// the cabinet and this page does not feel like switching products.
const layoutHTML = `
{{define "head"}}
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<link rel="icon" type="image/svg+xml" href="/logo.svg">
<style>
:root {
  color-scheme: light dark;
  --background: oklch(1 0 0);
  --foreground: oklch(0.145 0 0);
  --card: oklch(1 0 0);
  --primary: oklch(0.205 0 0);
  --primary-foreground: oklch(0.985 0 0);
  --muted: oklch(0.97 0 0);
  --muted-foreground: oklch(0.556 0 0);
  --destructive: oklch(0.577 0.245 27.325);
  --brand: oklch(0.511 0.262 276.966);
  --success: oklch(0.596 0.145 163.225);
  --success-subtle: oklch(0.979 0.021 166.113);
  --success-subtle-foreground: oklch(0.432 0.095 166.913);
  --border: oklch(0.922 0 0);
  --ring-card: oklch(0.145 0 0 / 10%);
  --radius: 0.625rem;
}
@media (prefers-color-scheme: dark) {
  :root {
    --background: oklch(0.145 0 0);
    --foreground: oklch(0.985 0 0);
    --card: oklch(0.205 0 0);
    --primary: oklch(0.922 0 0);
    --primary-foreground: oklch(0.205 0 0);
    --muted: oklch(0.269 0 0);
    --muted-foreground: oklch(0.708 0 0);
    --destructive: oklch(0.704 0.191 22.216);
    --brand: oklch(0.673 0.182 276.935);
    --success: oklch(0.765 0.177 163.223);
    --success-subtle: oklch(0.262 0.051 172.552);
    --success-subtle-foreground: oklch(0.845 0.143 164.978);
    --border: oklch(1 0 0 / 10%);
    --ring-card: oklch(0.985 0 0 / 10%);
  }
}
* { box-sizing: border-box; }
html { font-family: 'Geist Variable', 'Geist', system-ui, -apple-system, 'Segoe UI', sans-serif; }
body { margin: 0; min-height: 100vh; background: var(--background); color: var(--foreground); font-size: 14px; line-height: 1.5; }
a { color: var(--brand); text-decoration: none; }
a:hover { text-decoration: underline; }

header { position: sticky; top: 0; z-index: 40; border-bottom: 1px solid var(--border); background: color-mix(in oklch, var(--background) 80%, transparent); backdrop-filter: blur(8px); }
header > div { max-width: 48rem; margin: 0 auto; height: 4rem; padding: 0 1rem; display: flex; align-items: center; gap: 1rem; }
.logo { display: flex; align-items: center; gap: .5rem; font-weight: 600; font-size: 1.125rem; letter-spacing: -0.01em; color: var(--foreground); }
.logo img { width: 1.75rem; height: 1.75rem; }
.logo:hover, .product:hover { text-decoration: none; }
.product { font-weight: 700; font-size: 1.5rem; letter-spacing: -0.02em; color: var(--foreground); padding-left: 1rem; border-left: 1px solid var(--border); line-height: 2rem; }

main { max-width: 48rem; margin: 0 auto; padding: 2rem 1rem 3rem; }
h1 { font-size: 1.5rem; font-weight: 600; letter-spacing: -0.02em; margin: 0 0 .25rem; }
h2 { font-size: 1rem; font-weight: 500; margin: 0; }
.lead { color: var(--muted-foreground); margin: 0 0 1.5rem; }
.muted { color: var(--muted-foreground); }

.card { background: var(--card); border-radius: calc(var(--radius) * 1.4); box-shadow: 0 0 0 1px var(--ring-card); padding: 1rem; margin-bottom: 1rem; }
.card-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: .5rem; }
.card p { margin: .25rem 0; }

.row { display: flex; justify-content: space-between; align-items: center; gap: 1rem; padding: .6rem 0; border-top: 1px solid var(--border); }
.card-head + .row, .path + .row { border-top: none; }
.row .name { overflow-wrap: anywhere; min-width: 0; }
.row form, .row .tag, .row-actions { flex: none; }
.row-actions { display: flex; align-items: center; gap: .25rem; }
.icon-btn { display: inline-flex; align-items: center; justify-content: center; width: 2rem; height: 2rem; border-radius: var(--radius); color: var(--muted-foreground); }
.icon-btn:hover { background: var(--muted); color: var(--foreground); }
.row-main { display: flex; flex-direction: column; min-width: 0; }
.status { color: var(--muted-foreground); font-size: .8rem; }

.badge { display: inline-flex; align-items: center; gap: .35rem; border-radius: 999px; padding: .1rem .6rem; font-size: .8rem; font-weight: 500; background: var(--muted); color: var(--muted-foreground); }
.badge.ok { background: var(--success-subtle); color: var(--success-subtle-foreground); }
.badge.ok::before { content: ""; width: .45rem; height: .45rem; border-radius: 50%; background: var(--success); }
.alert-head { display: flex; align-items: center; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 1.5rem; }
h1.alert { display: flex; align-items: center; gap: .6rem; margin: 0; font-size: 1.875rem; color: var(--destructive); }
h1.alert svg { flex: none; }
.status-line.error { color: var(--destructive); font-weight: 500; }

.setting-row { display: flex; gap: 1rem; padding: .2rem 0; color: var(--muted-foreground); }
.setting-name { min-width: 9rem; font-weight: 500; color: var(--foreground); }

form.inline { display: inline; }
button, .btn { font: inherit; font-size: .875rem; font-weight: 500; display: inline-flex; align-items: center; justify-content: center; height: 2rem; padding: 0 .75rem; border-radius: var(--radius); border: 1px solid transparent; cursor: pointer; white-space: nowrap; text-decoration: none; background: var(--primary); color: var(--primary-foreground); }
button:hover, .btn:hover { background: color-mix(in oklch, var(--primary) 80%, transparent); text-decoration: none; }
button.lg, .btn.lg { height: 2.25rem; padding: 0 1rem; }
.btn.outline, button.outline { background: var(--background); color: var(--foreground); border-color: var(--border); }
.btn.outline:hover, button.outline:hover { background: var(--muted); }
button.ghost { background: transparent; color: var(--muted-foreground); }
button:disabled { opacity: .5; cursor: not-allowed; }
button.ghost:disabled:hover { background: transparent; color: var(--muted-foreground); }
button.ghost:hover { background: color-mix(in oklch, var(--destructive) 10%, transparent); color: var(--destructive); }
.actions { display: flex; gap: .5rem; flex-wrap: wrap; margin-top: 1rem; }

.path { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .8rem; color: var(--muted-foreground); overflow-wrap: anywhere; margin-bottom: .5rem; }
</style>
{{end}}

{{define "header"}}
<header>
  <div>
    <a class="logo" href="{{.HomeURL}}" target="_blank"><img src="/logo.svg" alt="">gate4.ai</a>
    <a class="product" href="/">Sync Client</a>
  </div>
</header>
{{end}}
`

// foldersListHTML is the "already configured" list — path, status, a gear
// to the mount's settings on gate4.ai, Remove —
// shared by the home page and the browse page. It has to keep showing up on
// browse too: a folder added five directories away would otherwise vanish
// from view the moment you're browsing anywhere else looking for another
// one to add.
const foldersListHTML = `
{{define "gear"}}<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z"/><circle cx="12" cy="12" r="3"/></svg>{{end}}

{{define "alert-icon"}}<svg xmlns="http://www.w3.org/2000/svg" width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><line x1="12" x2="12" y1="8" y2="12"/><line x1="12" x2="12.01" y1="16" y2="16"/></svg>{{end}}

{{define "folders-list"}}
<section class="card">
  <div class="card-head"><h2>Folders</h2></div>
  {{range .Folders}}
  <div class="row folder">
    <div class="row-main">
      <span class="name">{{.Path}}</span>
      <span class="status">{{if .FilesURL}}synced as <a href="{{.FilesURL}}" target="_blank">{{.Slug}}</a>{{else}}{{.Status}}{{end}}</span>
    </div>
    <div class="row-actions">
      {{if .SettingsURL}}<a class="icon-btn" href="{{.SettingsURL}}" target="_blank" title="Settings for {{.Slug}} on gate4.ai" aria-label="Settings for {{.Slug}}">{{template "gear"}}</a>{{end}}
      <form class="inline" method="post" action="/folders/remove">
        <input type="hidden" name="id" value="{{.ID}}">
        <button class="ghost" type="submit"{{if not .CanRemove}} disabled title="Connect to gate4.ai to remove this folder"{{end}}>Remove</button>
      </form>
    </div>
  </div>
  {{else}}
  <p class="muted">No folders yet.</p>
  {{end}}
</section>
{{end}}
`

const homeHTML = `<!doctype html>
<html lang="en">
<head>
{{template "head"}}
<title>gate4.ai Sync Client</title>
{{if not .Linked}}<meta http-equiv="refresh" content="1">{{end}}
</head>
<body>
{{template "header" .}}
<main>
{{if .Linked}}
<h1>Connected to {{.VaultSlug}}</h1>
<p class="lead status-line{{if .StatusIsError}} error{{end}}">Status: {{.Status}}</p>
{{else if .Reconnect}}
<div class="alert-head">
  <h1 class="alert">{{template "alert-icon"}}Not connected</h1>
  <form method="post" action="/save">
    <button class="lg" type="submit">Connect to gate4.ai</button>
  </form>
</div>
{{else}}
<h1>Not connected yet</h1>
<p class="lead">Add a folder to sync — gate4.ai will then ask you to connect this computer to your account.</p>
{{end}}

{{template "folders-list" .}}

<p class="actions"><a class="btn outline" href="/browse">+ Add a folder</a></p>

{{if .Settings}}
<section class="card settings">
  <div class="card-head"><h2>Server settings</h2></div>
  {{range .Settings}}
  <div class="setting-row"><span class="setting-name">{{.Name}}</span> {{.Value}}</div>
  {{end}}
</section>
{{end}}

</main>
</body>
</html>
`

const browseHTML = `<!doctype html>
<html lang="en">
<head>
{{template "head"}}
<title>Choose a folder — gate4.ai Sync Client</title>
</head>
<body>
{{template "header" .}}
<main>
<h1>Choose a folder</h1>
<p class="lead">Pick a folder to sync with gate4.ai.</p>

{{template "folders-list" .}}

<section class="card">
  <p class="path">{{.Path}}</p>
  {{if .Parent}}<div class="row"><a class="name" href="/browse?path={{.Parent}}">.. (up)</a></div>{{end}}
  {{range .Entries}}
  <div class="row entry">
    <a class="name" href="/browse?path={{.Path}}">{{.Name}}/</a>
    {{if .Synced}}
    <span class="badge ok tag">Already syncing</span>
    {{else}}
    <form method="post" action="/folders">
      <input type="hidden" name="path" value="{{.Path}}">
      <button type="submit">Sync</button>
    </form>
    {{end}}
  </div>
  {{else}}
  <p class="muted">No subfolders here.</p>
  {{end}}
</section>

<p class="actions"><a class="btn outline" href="/">Cancel</a></p>
</main>
</body>
</html>
`

const errorHTML = `<!doctype html>
<html lang="en">
<head>
{{template "head"}}
<title>{{.Title}} — gate4.ai Sync Client</title>
</head>
<body>
{{template "header" .}}
<main>
<h1 class="alert">{{template "alert-icon"}}{{.Title}}</h1>
<p class="lead">{{.Message}}</p>
<p class="actions"><a class="btn outline" href="/">Back</a></p>
</main>
</body>
</html>
`
