package main

import "html/template"

type formData struct {
	Message   string
	Languages []string
}

type viewData struct {
	ID       string
	Language string
	Created  string
	Body     template.HTML
}

type listRow struct {
	ID       string
	Language string
	Created  string
	Expires  string
	Preview  string
	Lines    int
}

type listData struct {
	Rows []listRow
}

const baseCSS = `
:root { color-scheme: light; }
* { box-sizing: border-box; }
body {
	font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
	margin: 0;
	padding: 0;
	background: #fafafa;
	color: #1a1a1a;
}
textarea, select, input { color: #1a1a1a; }
header {
	padding: 14px 24px;
	border-bottom: 1px solid #e4e4e4;
	background: #fff;
	display: flex;
	align-items: center;
	justify-content: space-between;
}
header a { color: inherit; text-decoration: none; font-weight: 600; }
header .meta { color: #777; font-size: 13px; font-weight: 400; }
main { max-width: 1100px; margin: 0 auto; padding: 24px; }
form { display: flex; flex-direction: column; gap: 12px; }
textarea {
	width: 100%;
	min-height: 70vh;
	padding: 12px;
	font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
	font-size: 13px;
	border: 1px solid #d0d0d0;
	border-radius: 6px;
	background: #fff;
	resize: vertical;
}
.controls { display: flex; gap: 10px; align-items: center; }
select, button {
	font: inherit;
	padding: 8px 14px;
	border: 1px solid #d0d0d0;
	border-radius: 6px;
	background: #fff;
}
button {
	background: #1f6feb;
	color: #fff;
	border-color: #1f6feb;
	cursor: pointer;
	font-weight: 600;
}
button:hover { background: #1a5fd1; }
.hint { color: #888; font-size: 12px; }
pre { margin: 0; }
.paste-view { background: #fff; border: 1px solid #e4e4e4; border-radius: 6px; overflow: auto; }
.paste-view pre { padding: 16px; font-size: 13px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.actions a { margin-left: 12px; color: #1f6feb; text-decoration: none; font-size: 13px; }
.actions a:hover { text-decoration: underline; }
table.pastes { width: 100%; border-collapse: collapse; background: #fff; border: 1px solid #e4e4e4; border-radius: 6px; overflow: hidden; }
table.pastes th, table.pastes td { text-align: left; padding: 10px 14px; border-bottom: 1px solid #f0f0f0; font-size: 13px; vertical-align: top; }
table.pastes th { background: #f7f7f7; font-weight: 600; color: #555; font-size: 12px; text-transform: uppercase; letter-spacing: 0.04em; }
table.pastes tr:last-child td { border-bottom: none; }
table.pastes tr:hover td { background: #fafbfc; }
table.pastes td.id a { color: #1f6feb; text-decoration: none; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-weight: 600; }
table.pastes td.id a:hover { text-decoration: underline; }
table.pastes td.preview { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; color: #555; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 480px; }
table.pastes td.lang { color: #777; font-size: 12px; }
table.pastes td.meta { color: #999; font-size: 12px; white-space: nowrap; }
.empty { padding: 40px; text-align: center; color: #888; background: #fff; border: 1px solid #e4e4e4; border-radius: 6px; }
form.inline { display: inline; margin: 0; padding: 0; }
button.link {
	background: none; border: none; padding: 0;
	color: #d1242f; font: inherit; font-size: 13px;
	cursor: pointer; text-decoration: none;
}
button.link:hover { text-decoration: underline; }
table.pastes button.link { font-size: 12px; }
`

const formHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>paste</title>
<style>` + baseCSS + `</style>
</head>
<body>
<header>
	<a href="/">paste</a>
	<span class="meta">
		local · 30-day ttl
		<span class="actions"><a href="/list">recent</a></span>
	</span>
</header>
<main>
	<form method="POST" action="/">
		<textarea name="content" autofocus placeholder="Paste anything. Cmd+Enter to submit."></textarea>
		<div class="controls">
			<label for="language" class="hint">Language</label>
			<select name="language" id="language">
				{{- range .Languages }}
				<option value="{{.}}">{{if .}}{{.}}{{else}}auto-detect{{end}}</option>
				{{- end }}
			</select>
			<button type="submit">Create paste</button>
			<span class="hint">expires in 30 days</span>
		</div>
	</form>
</main>
<script>
document.querySelector('textarea').addEventListener('keydown', function (e) {
	if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
		e.preventDefault();
		this.form.submit();
	}
});
</script>
</body>
</html>
`

const viewHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.ID}} · paste</title>
<style>` + baseCSS + `</style>
</head>
<body>
<header>
	<a href="/">paste</a>
	<span class="meta">
		{{.ID}} · {{.Language}} · {{.Created}}
		<span class="actions">
			<a href="/{{.ID}}/raw">raw</a>
			<a href="/list">recent</a>
			<a href="/">new</a>
			<form class="inline" method="POST" action="/{{.ID}}/delete" onsubmit="return confirm('Delete {{.ID}}?');">
				<button type="submit" class="link">delete</button>
			</form>
		</span>
	</span>
</header>
<main>
	<div class="paste-view">{{.Body}}</div>
</main>
</body>
</html>
`

const listHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>recent · paste</title>
<style>` + baseCSS + `</style>
</head>
<body>
<header>
	<a href="/">paste</a>
	<span class="meta">
		recent pastes
		<span class="actions"><a href="/">new</a></span>
	</span>
</header>
<main>
	{{if .Rows}}
	<table class="pastes">
		<thead>
			<tr><th>id</th><th>lang</th><th>preview</th><th>lines</th><th>created</th><th>expires</th><th></th></tr>
		</thead>
		<tbody>
		{{- range .Rows }}
			<tr>
				<td class="id"><a href="/{{.ID}}">{{.ID}}</a></td>
				<td class="lang">{{if .Language}}{{.Language}}{{else}}—{{end}}</td>
				<td class="preview">{{.Preview}}</td>
				<td class="meta">{{.Lines}}</td>
				<td class="meta">{{.Created}}</td>
				<td class="meta">{{.Expires}}</td>
				<td class="meta">
					<form class="inline" method="POST" action="/{{.ID}}/delete" onsubmit="return confirm('Delete {{.ID}}?');">
						<button type="submit" class="link">delete</button>
					</form>
				</td>
			</tr>
		{{- end }}
		</tbody>
	</table>
	{{else}}
	<div class="empty">no pastes yet. <a href="/">create one</a>.</div>
	{{end}}
</main>
</body>
</html>
`
