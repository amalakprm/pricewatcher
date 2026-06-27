package web

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"pricewatcher/internal/config"
	"pricewatcher/internal/db"
)

//go:embed templates/*.html templates/components/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// LogRingBuffer stores structured LogEntry records in memory.
type LogRingBuffer struct {
	mu    sync.RWMutex
	lines []LogEntry
	limit int
	head  int
}

// NewLogRingBuffer initializes the log ring buffer.
func NewLogRingBuffer(limit int) *LogRingBuffer {
	return &LogRingBuffer{
		lines: make([]LogEntry, 0, limit),
		limit: limit,
	}
}

// Write decodes incoming JSON logs and appends them to the in-memory buffer.
func (b *LogRingBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var entry LogEntry
	if err := json.Unmarshal(p, &entry); err != nil {
		// Fallback for raw, unstructured output logs
		entry = LogEntry{
			Time:    time.Now(),
			Level:   "INFO",
			Message: strings.TrimSuffix(string(p), "\n"),
		}
	}

	if len(b.lines) < b.limit {
		b.lines = append(b.lines, entry)
	} else {
		b.lines[b.head] = entry
		b.head = (b.head + 1) % b.limit
	}
	return len(p), nil
}

// GetEntries returns all buffered log entries in chronological order.
func (b *LogRingBuffer) GetEntries() []LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	res := make([]LogEntry, 0, len(b.lines))
	if len(b.lines) < b.limit {
		res = append(res, b.lines...)
	} else {
		res = append(res, b.lines[b.head:]...)
		res = append(res, b.lines[:b.head]...)
	}
	return res
}

// Server acts as the web console execution context.
type Server struct {
	db        *db.DB
	cfg       *config.Config
	cron      *cron.Cron
	logBuffer *LogRingBuffer
	templates *template.Template
	running   bool
	runningMu sync.Mutex
}

// NewServer registers custom functions and compiles page templates using wildcards.
func NewServer(database *db.DB, cfg *config.Config, cr *cron.Cron, logBuffer *LogRingBuffer) *Server {
	funcMap := template.FuncMap{
		"base64": func(s string) string {
			return base64.URLEncoding.EncodeToString([]byte(s))
		},
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	}

	// Parse using wildcards so addition of files/components doesn't require hardcoding here
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(
		templatesFS,
		"templates/*.html",
		"templates/components/*.html",
	))

	return &Server{
		db:        database,
		cfg:       cfg,
		cron:      cr,
		logBuffer: logBuffer,
		templates: tmpl,
	}
}

// Routes configures URL mapping to Server handler actions.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Serve embedded static files
	staticSubFS, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))

	// Web UI HTML pages
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /products", s.handleProducts)
	mux.HandleFunc("GET /product/{url_b64}", s.handleProductDetail)
	mux.HandleFunc("GET /logs", s.handleLogs)
	mux.HandleFunc("GET /settings", s.handleSettings)

	// API endpoints
	mux.HandleFunc("POST /api/run", s.handleAPIRun)
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)
	mux.HandleFunc("GET /api/products", s.handleAPIProducts)
	mux.HandleFunc("POST /api/products", s.handleAddProduct)
	mux.HandleFunc("DELETE /api/products/{id}", s.handleDeleteProduct)
	mux.HandleFunc("PATCH /api/products/{id}", s.handleUpdateProductTarget)
	mux.HandleFunc("POST /api/products/{id}/toggle", s.handleToggleProductActive)
	mux.HandleFunc("POST /api/feed/sync", s.handleFeedSync)
	mux.HandleFunc("GET /logs/run/{id}", s.handleRunDetailsPartial)
	mux.HandleFunc("GET /health", s.handleHealth)

	return mux
}
