package webui

// templates are plain Go html/template strings, embedded rather than read
// from disk — this is a single-binary CLI tool, not a web app with a
// deploy step that could lose a sibling assets/ directory.
var templates = map[string]string{
	"home":   homeHTML + foldersListHTML,
	"browse": browseHTML + foldersListHTML,
}

// foldersListHTML is the "already configured" list — path, status, Remove —
// shared by the home page and the browse page. It has to keep showing up on
// browse too: a folder added five directories away would otherwise vanish
// from view the moment you're browsing anywhere else looking for another
// one to add.
const foldersListHTML = `
{{define "folders-list"}}
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
{{end}}
`

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

{{template "folders-list" .}}

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
.path { color: #666; font-size: .85rem; margin-bottom: 1rem; }
.entry { display: flex; justify-content: space-between; align-items: center; padding: .4rem 0; border-bottom: 1px solid #eee; }
.entry .name { overflow-wrap: anywhere; }
.entry form { flex: none; margin-left: 1rem; }
.synced { color: #666; font-size: .85rem; flex: none; margin-left: 1rem; }
.up { padding: .4rem 0; }
button, .btn { font: inherit; padding: .3rem .8rem; }
.btn { display: inline-block; text-decoration: none; color: inherit; border: 1px solid #ccc; border-radius: 3px; }
.acts { margin-top: 1.5rem; }
.folder { display: flex; justify-content: space-between; align-items: center; padding: .5rem 0; border-bottom: 1px solid #ddd; }
.status { color: #666; font-size: .85rem; }
form.inline { display: inline; }
.muted { color: #666; }
h2 { font-size: 1rem; margin-top: 2rem; }
hr { border: none; border-top: 1px solid #ddd; margin: 1.5rem 0; }
</style>
</head>
<body>
<h1>Choose a folder</h1>

{{template "folders-list" .}}
<hr>

<p class="path">{{.Path}}</p>

{{if .Parent}}<div class="up"><a href="/browse?path={{.Parent}}">.. (up)</a></div>{{end}}
{{range .Entries}}
<div class="entry">
  <a class="name" href="/browse?path={{.Path}}">{{.Name}}/</a>
  {{if .Synced}}
  <span class="synced">Already syncing</span>
  {{else}}
  <form method="post" action="/folders">
    <input type="hidden" name="path" value="{{.Path}}">
    <button type="submit">Sync</button>
  </form>
  {{end}}
</div>
{{else}}
<p>No subfolders here.</p>
{{end}}

<p class="acts"><a class="btn" href="/">Cancel</a></p>
</body>
</html>
`
