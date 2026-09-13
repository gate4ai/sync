package webui

// templates are plain Go html/template strings, embedded rather than read
// from disk — this is a single-binary CLI tool, not a web app with a
// deploy step that could lose a sibling assets/ directory.
var templates = map[string]string{
	"home":   homeHTML,
	"browse": browseHTML,
}

const homeHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>gate4.ai sync</title>
{{if not .Linked}}<meta http-equiv="refresh" content="3">{{end}}
<style>
body { font: 14px system-ui, sans-serif; max-width: 640px; margin: 2rem auto; padding: 0 1rem; color: #1a1a1a; }
h1 { font-size: 1.2rem; }
.folder { display: flex; justify-content: space-between; align-items: center; padding: .5rem 0; border-bottom: 1px solid #ddd; }
.status { color: #666; font-size: .85rem; }
.settings { color: #666; font-size: .85rem; margin: 1rem 0; }
form.inline { display: inline; }
button, input[type=submit] { font: inherit; padding: .3rem .8rem; }
.muted { color: #666; }
</style>
</head>
<body>
<h1>gate4.ai sync</h1>

{{if .Linked}}
<p>Connected to vault <strong>{{.VaultSlug}}</strong>.
  <a href="{{.CabinetURL}}" target="_blank">Open cabinet</a></p>
{{else}}
<p class="muted">Not connected yet. Add the folders you want to sync, then press Connect.</p>
{{end}}

<h2>Folders</h2>
{{range .Folders}}
<div class="folder">
  <span>{{.Path}}</span>
  <span class="status">{{.Status}}</span>
  <form class="inline" method="post" action="/folders/remove">
    <input type="hidden" name="id" value="{{.ID}}">
    <button type="submit">Remove</button>
  </form>
</div>
{{else}}
<p class="muted">No folders yet.</p>
{{end}}

<p><a href="/browse">+ Add a folder</a></p>

{{if .Settings}}
<div class="settings">
  <strong>Server settings</strong> (change these in the cabinet):<br>
  Allowed types: {{.Settings.AllowedExtensions}} ·
  Max file size: {{.Settings.MaxFileSize}} ·
  Poll interval: {{.Settings.PollInterval}}
</div>
{{end}}

{{if not .Linked}}
<form method="post" action="/save">
  <button type="submit">Connect to gate4.ai</button>
</form>
{{end}}
</body>
</html>
`

const browseHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Choose a folder — gate4.ai sync</title>
<style>
body { font: 14px system-ui, sans-serif; max-width: 640px; margin: 2rem auto; padding: 0 1rem; }
.entry { padding: .3rem 0; }
.path { color: #666; font-size: .85rem; }
</style>
</head>
<body>
<h1>Choose a folder</h1>
<p class="path">{{.Path}}</p>

<form method="post" action="/folders">
  <input type="hidden" name="path" value="{{.Path}}">
  <button type="submit">Sync this folder</button>
</form>

<p>
{{if .Parent}}<a href="/browse?path={{.Parent}}">.. (up)</a>{{end}}
</p>
{{range .Entries}}
<div class="entry"><a href="/browse?path={{.Path}}">{{.Name}}/</a></div>
{{else}}
<p>No subfolders here.</p>
{{end}}

<p><a href="/">&larr; Back</a></p>
</body>
</html>
`
