package main

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	_ "modernc.org/sqlite"
)

const (
	ttl         = 30 * 24 * time.Hour
	maxBodySize = 5 << 20
)

var (
	db      *sql.DB
	tplForm *template.Template
	tplView *template.Template
	tplList *template.Template
)

func main() {
	addr := flag.String("addr", ":80", "listen address")
	dbPath := flag.String("db", "/usr/local/var/paste/pastes.db", "sqlite db path")
	flag.Parse()

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o755); err != nil {
		log.Fatalf("mkdir db dir: %v", err)
	}

	var err error
	db, err = sql.Open("sqlite", *dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := initSchema(); err != nil {
		log.Fatalf("init schema: %v", err)
	}

	tplForm = template.Must(template.New("form").Parse(formHTML))
	tplView = template.Must(template.New("view").Parse(viewHTML))
	tplList = template.Must(template.New("list").Parse(listHTML))

	prune()
	go pruneLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", handleHealth)
	mux.HandleFunc("/list", handleList)
	mux.HandleFunc("/", handleRoot)

	log.Printf("paste listening on %s, db=%s", *addr, *dbPath)
	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}

func initSchema() error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pastes (
			id         TEXT PRIMARY KEY,
			content    TEXT NOT NULL,
			language   TEXT,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_expires ON pastes(expires_at);
	`)
	return err
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")

	if path == "" {
		switch r.Method {
		case http.MethodGet:
			renderForm(w, "")
		case http.MethodPost:
			handleCreate(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	id, rest, _ := strings.Cut(path, "/")
	if !validID(id) {
		http.NotFound(w, r)
		return
	}

	switch rest {
	case "":
		if r.Method == http.MethodDelete {
			handleDelete(w, r, id, false)
			return
		}
		handleView(w, r, id)
	case "raw":
		handleRaw(w, r, id)
	case "delete":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleDelete(w, r, id, true)
	default:
		http.NotFound(w, r)
	}
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodySize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}

	content := ""
	lang := r.URL.Query().Get("lang")

	ct := r.Header.Get("Content-Type")
	isMultipart := strings.HasPrefix(ct, "multipart/form-data")
	isURLEncoded := strings.HasPrefix(ct, "application/x-www-form-urlencoded")

	if isMultipart {
		// Re-parse multipart from the read body.
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		if err := r.ParseMultipartForm(maxBodySize); err == nil {
			content = r.FormValue("content")
			if v := r.FormValue("language"); v != "" {
				lang = v
			}
		}
	} else if isURLEncoded {
		if values, err := parseForm(string(body)); err == nil {
			if c := values.Get("content"); c != "" {
				content = c
			}
			if v := values.Get("language"); v != "" {
				lang = v
			}
		}
	}

	// If we didn't extract content from a form, treat the whole body as the paste.
	// Covers: curl --data-binary @file, raw text/plain POSTs, etc.
	if content == "" {
		content = string(body)
	}

	if strings.TrimSpace(content) == "" {
		http.Error(w, "empty paste", http.StatusBadRequest)
		return
	}

	id, err := insertPaste(content, lang)
	if err != nil {
		log.Printf("insert: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	url := "/" + id
	if isBrowser(r) {
		http.Redirect(w, r, url, http.StatusSeeOther)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "paste"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "%s://%s%s\n", scheme, host, url)
}

func handleView(w http.ResponseWriter, r *http.Request, id string) {
	content, lang, createdAt, ok, err := getPaste(id)
	if err != nil {
		log.Printf("get: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		log.Printf("tokenise: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	formatter := html.New(html.WithLineNumbers(true), html.TabWidth(4), html.WithClasses(false))

	var sb strings.Builder
	if err := formatter.Format(&sb, style, iterator); err != nil {
		log.Printf("format: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := viewData{
		ID:       id,
		Language: lexer.Config().Name,
		Created:  time.Unix(createdAt, 0).Format("2006-01-02 15:04"),
		Body:     template.HTML(sb.String()),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tplView.Execute(w, data); err != nil {
		log.Printf("template view: %v", err)
	}
}

func handleList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT id, COALESCE(language,''), created_at, expires_at, content
		FROM pastes
		WHERE expires_at > ?
		ORDER BY created_at DESC
	`, time.Now().Unix())
	if err != nil {
		log.Printf("list: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var data listData
	for rows.Next() {
		var row listRow
		var created, expires int64
		var content string
		if err := rows.Scan(&row.ID, &row.Language, &created, &expires, &content); err != nil {
			log.Printf("list scan: %v", err)
			continue
		}
		row.Created = time.Unix(created, 0).Format("2006-01-02 15:04")
		row.Expires = humanUntil(time.Unix(expires, 0))
		row.Lines = strings.Count(content, "\n") + 1
		row.Preview = firstLine(content, 120)
		data.Rows = append(data.Rows, row)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tplList.Execute(w, data); err != nil {
		log.Printf("template list: %v", err)
	}
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func humanUntil(t time.Time) string {
	d := time.Until(t)
	if d < 0 {
		return "expired"
	}
	days := int(d.Hours() / 24)
	if days > 1 {
		return fmt.Sprintf("in %dd", days)
	}
	hours := int(d.Hours())
	if hours >= 1 {
		return fmt.Sprintf("in %dh", hours)
	}
	return fmt.Sprintf("in %dm", int(d.Minutes()))
}

func handleDelete(w http.ResponseWriter, r *http.Request, id string, redirect bool) {
	res, err := db.Exec(`DELETE FROM pastes WHERE id = ?`, id)
	if err != nil {
		log.Printf("delete: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	log.Printf("deleted %s", id)
	if redirect {
		http.Redirect(w, r, "/list", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "deleted %s\n", id)
}

func handleRaw(w http.ResponseWriter, r *http.Request, id string) {
	content, _, _, ok, err := getPaste(id)
	if err != nil {
		log.Printf("get: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(content))
}

func renderForm(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tplForm.Execute(w, formData{Message: msg, Languages: supportedLanguages()})
}

func insertPaste(content, lang string) (string, error) {
	now := time.Now().Unix()
	exp := time.Now().Add(ttl).Unix()

	for attempt := 0; attempt < 10; attempt++ {
		id, err := newID()
		if err != nil {
			return "", err
		}
		_, err = db.Exec(
			`INSERT INTO pastes (id, content, language, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
			id, content, lang, now, exp,
		)
		if err == nil {
			return id, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			return "", err
		}
	}
	return "", errors.New("could not allocate unique id after 10 attempts")
}

func getPaste(id string) (content, lang string, createdAt int64, ok bool, err error) {
	row := db.QueryRow(
		`SELECT content, COALESCE(language, ''), created_at FROM pastes WHERE id = ? AND expires_at > ?`,
		id, time.Now().Unix(),
	)
	err = row.Scan(&content, &lang, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, false, nil
	}
	if err != nil {
		return "", "", 0, false, err
	}
	return content, lang, createdAt, true, nil
}

func newID() (string, error) {
	// 8-digit random number in [10000000, 99999999]
	n, err := rand.Int(rand.Reader, big.NewInt(90000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("P%08d", n.Int64()+10000000), nil
}

func validID(s string) bool {
	if len(s) != 9 || s[0] != 'P' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func prune() {
	res, err := db.Exec(`DELETE FROM pastes WHERE expires_at <= ?`, time.Now().Unix())
	if err != nil {
		log.Printf("prune: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("pruned %d expired pastes", n)
	}
}

func pruneLoop() {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for range t.C {
		prune()
	}
}

func isBrowser(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

// parseForm decodes a urlencoded body. Returns an error if the body contains
// keys without "=" separators (i.e. looks like raw text, not a form).
func parseForm(body string) (url.Values, error) {
	if body == "" {
		return url.Values{}, nil
	}
	for _, pair := range strings.Split(body, "&") {
		if !strings.Contains(pair, "=") {
			return nil, fmt.Errorf("not form-encoded")
		}
	}
	return url.ParseQuery(body)
}

func supportedLanguages() []string {
	// A curated shortlist for the dropdown; Chroma supports many more via auto-detect.
	return []string{
		"", "go", "python", "javascript", "typescript", "java", "c", "cpp",
		"rust", "ruby", "php", "hack", "shell", "bash", "sql", "json", "yaml",
		"toml", "html", "css", "markdown", "diff",
	}
}
