// Package api offers the HTTP REST surface for authentication and file CRUD.
package api

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shiyi/starstack/internal/auth"
	"github.com/shiyi/starstack/internal/config"
	"github.com/shiyi/starstack/internal/fsops"
	"github.com/shiyi/starstack/internal/preview"
	"github.com/shiyi/starstack/internal/store"
	"github.com/shiyi/starstack/internal/trash"
)

// Server wires config, storage and auth into an HTTP handler.
type Server struct {
	cfg   *config.Config
	store *store.Store
	auth  *auth.Service
	fu    *fsops.Manager // personal space
	fsu   *fsops.Manager // shared/public disk
	trash *trash.Service
	prev  *preview.Service
	log   *slog.Logger
}

// New constructs the Server and its dependency managers.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*Server, error) {
	fu, err := fsops.New(filepath.Join(cfg.DataDir, "users"))
	if err != nil {
		return nil, fmt.Errorf("personal space init: %w", err)
	}
	fsu, err := fsops.New(cfg.ShareRoot)
	if err != nil {
		return nil, fmt.Errorf("shared space init: %w", err)
	}
	ts, err := trash.New(st, cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("trash init: %w", err)
	}
	pv, err := preview.New(preview.Options{CacheDir: filepath.Join(cfg.DataDir, "cache"), MemoryEntries: 512})
	if err != nil {
		return nil, fmt.Errorf("preview init: %w", err)
	}
	return &Server{
		cfg: cfg, store: st, log: log,
		auth: auth.New(st, cfg.Secret),
		fu:   fu, fsu: fsu, trash: ts, prev: pv,
	}, nil
}

// Routes returns the configured chi router.
func (s *Server) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cors)

	r.Route("/api", func(r chi.Router) {
		// Public
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/refresh", s.handleRefresh)
		r.Get("/health", s.handleHealth)

		// Board controls (used to avoid duplicating users)
		r.Get("/users/first", s.handleFirstUser)

		// Preview resources self-authenticate (accept token via header OR ?auth=
		// query param) because <img>/<video>/<iframe> cannot send headers.
		r.Get("/preview/thumb", s.handleThumb)
		r.Get("/preview/raw", s.handlePreviewRaw)

		// Authenticated
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Post("/auth/logout", s.handleLogout)

			r.Post("/users", s.handleCreateUser)
			r.Get("/users", s.handleListUsers)
			r.Patch("/users/{id}/state", s.handleSetUserState)
			r.Post("/users/{id}/password", s.handleResetPassword)
			r.Get("/me", s.handleMe)

			r.Get("/fs/list", s.handleList)
			r.Post("/fs/mkdir", s.handleMkdir)
			r.Post("/fs/rename", s.handleRename)
			r.Post("/fs/move", s.handleMove)
			r.Post("/fs/copy", s.handleCopy)
			r.Post("/fs/delete", s.handleDelete)
			r.Post("/fs/upload", s.handleUpload)
			r.Get("/fs/download", s.handleDownload)
			r.Get("/fs/zip", s.handleZip)
			r.Post("/preview/rotate", s.handleRotate)

			r.Get("/trash/list", s.handleTrashList)
			r.Post("/trash/restore", s.handleTrashRestore)
			r.Post("/trash/purge", s.handleTrashPurge)
			r.Post("/trash/empty", s.handleTrashEmpty)

			r.Get("/share/list", s.handleListShares)
			r.Post("/share", s.handleCreateShare)
			r.Delete("/share/{token}", s.handleDeleteShare)
		})

		// Public share token access (no auth)
		r.Get("/share/{token}/download", s.handleShareToken(s.handleDownload))
		r.Get("/share/{token}/list", s.handleShareToken(s.handleList))
	})

	return r
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization,Content-Type,Range")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func (s *Server) mgrFor(scope string) (*fsops.Manager, error) {
	switch scope {
	case "", "me":
		return s.fu, nil
	case "share":
		return s.fsu, nil
	}
	return nil, fmt.Errorf("invalid scope %q", scope)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) currentUser(r *http.Request) *store.User {
	return r.Context().Value(ctxKeyUser).(*store.User)
}

// ctxKey is a private type for request context values.
type ctxKeyType int

const ctxKeyUser ctxKeyType = 0

// handleHealth reports liveness + version.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- auth handlers ----

// handleFirstUser reports whether any user exists (to UI bootstrap admin).
func (s *Server) handleFirstUser(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"has_users": n > 0})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	// First registered user is the admin.
	admin := false
	count, err := s.store.CountUsers()
	if err == nil && count == 0 {
		admin = true
	}
	if err := s.ensureUser(req.Username, req.Password, admin); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	access, refresh, u, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrUnauth) || errors.Is(err, auth.ErrInvalid) {
			writeErr(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		writeErr(w, http.StatusForbidden, "account disabled")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "refresh_token": refresh,
		"user": map[string]any{"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin},
	})
}

// ensureUser creates the user on first run deterministically (idempotent).
func (s *Server) ensureUser(username, password string, admin bool) error {
	_, err := s.store.GetUserByName(username)
	if errors.Is(err, store.ErrNotFound) {
		hash, err := auth.HashPassword(password)
		if err != nil {
			return fmt.Errorf("hash error")
		}
		if _, err := s.store.CreateUser(username, hash, admin); err != nil {
			return fmt.Errorf("user create failed")
		}
	}
	return nil
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	access, refresh, u, err := s.auth.Refresh(req.RefreshToken)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "refresh_token": refresh,
		"user": map[string]any{"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.RefreshToken != "" {
		_ = s.auth.Logout(req.RefreshToken)
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireAuth guards authenticated routes.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ah := r.Header.Get("Authorization")
		token := strings.TrimPrefix(ah, "Bearer ")
		if token == "" || token == ah {
			writeErr(w, http.StatusUnauthorized, "missing token")
			return
		}
		u, err := s.auth.ParseAccessToken(token)
		if err != nil || u == nil {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		ctx := contextWithUser(r.Context(), u)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func contextWithUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// ---- user management ----

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	me := s.currentUser(r)
	if !me.IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Admin    bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	if _, err := s.store.CreateUser(req.Username, hash, req.Admin); err != nil {
		writeErr(w, http.StatusConflict, "username taken")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.currentUser(r).IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	users, err := s.store.ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]any{
			"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin, "disabled": u.Disabled,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// handleMe returns the current user's profile plus their storage usage.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	me := s.currentUser(r)
	_, err := s.fu.Resolve("/")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fs error")
		return
	}
	entries, err := s.fu.List("/")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "fs error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": me.ID, "username": me.Username, "is_admin": me.IsAdmin, "disabled": me.Disabled,
		"scope_me": "/", "items": len(entries),
	})
}

// handleSetUserState enables/disables a user (admin only).
func (s *Server) handleSetUserState(w http.ResponseWriter, r *http.Request) {
	if !s.currentUser(r).IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req struct {
		Disabled *bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Disabled == nil {
		writeErr(w, http.StatusBadRequest, "disabled required")
		return
	}
	if err := s.store.SetDisabled(id, *req.Disabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleResetPassword resets a user's password (admin only).
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.currentUser(r).IsAdmin {
		writeErr(w, http.StatusForbidden, "admin only")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "password required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash error")
		return
	}
	if err := s.store.UpdatePassword(id, hash); err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- file operations ----

func (s *Server) fileParams(r *http.Request) (scope, virtual string, err error) {
	scope = r.URL.Query().Get("scope")
	if scope == "" {
		scope = "me"
	}
	virtual = r.URL.Query().Get("path")
	if virtual == "" {
		virtual = "/"
	}
	return scope, virtual, nil
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	scope, virtual, _ := s.fileParams(r)
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	entries, err := mgr.List(virtual)
	if err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": virtual, "entries": entries})
}

func (s *Server) handleMkdir(w http.ResponseWriter, r *http.Request) {
	scope, virtual, _ := s.fileParams(r)
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := mgr.Mkdir(virtual); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope   string `json:"scope"`
		Path    string `json:"path"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := mgr.Rename(req.Path, req.NewName); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope     string `json:"scope"`
		Path      string `json:"path"`
		NewParent string `json:"new_parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := mgr.Move(req.Path, req.NewParent); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope string `json:"scope"`
		Path  string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// Delete moves the item into the acting user's trash (recoverable).
	if err := s.trash.Trash(s.currentUser(r).ID, mgr, req.Scope, req.Path); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "trashed"})
}

// handleTrashList returns the trash for the calling user.
func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	entries, err := s.trash.List(s.currentUser(r).ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trash": entries})
}

// handleTrashRestore moves an item back to its original location.
func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.trash.Restore(s.currentUser(r).ID, req.ID, s.mgrFor); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restored"})
}

// handleTrashPurge permanently deletes a single trash item.
func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if err := s.trash.Purge(s.currentUser(r).ID, req.ID); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTrashEmpty permanently deletes all trash for the user.
func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	n, err := s.trash.Empty(s.currentUser(r).ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"purged": n})
}

// handleUpload streams an uploaded file into the target directory.
// Form fields: dir (destination virtual dir), scope; files under "files".
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "me"
	}
	dir := r.FormValue("dir")
	if dir == "" {
		dir = "/"
	}
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	destDir, err := mgr.Resolve(dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid destination")
		return
	}
	if fi, err := os.Stat(destDir); err != nil || !fi.IsDir() {
		writeErr(w, http.StatusNotFound, "destination not found")
		return
	}
	if err := r.ParseMultipartForm(s.cfg.MaxUploadMB * 1024 * 1024); err != nil {
		writeErr(w, http.StatusBadRequest, "upload too large")
		return
	}
	files := r.MultipartForm.File["files"]
	result := []map[string]any{}
	for _, fh := range files {
		src, err := fh.Open()
		if err != nil {
			result = append(result, map[string]any{"name": fh.Filename, "error": "open failed"})
			continue
		}
		dstPath := filepath.Join(destDir, filepath.Base(fh.Filename))
		dst, err := os.Create(dstPath)
		if err != nil {
			result = append(result, map[string]any{"name": fh.Filename, "error": "create failed"})
			src.Close()
			continue
		}
		n, werr := io.Copy(dst, src)
		dst.Close()
		src.Close()
		if werr != nil {
			_ = os.Remove(dstPath)
			result = append(result, map[string]any{"name": fh.Filename, "error": "write failed"})
			continue
		}
		result = append(result, map[string]any{"name": fh.Filename, "size": n, "ok": true})
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": result})
}

// handleCopy duplicates a path into a target directory.
func (s *Server) handleCopy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope     string `json:"scope"`
		Path      string `json:"path"`
		NewParent string `json:"new_parent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := mgr.Copy(req.Path, req.NewParent); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleZip streams a zip archive of the requested paths (multi-select download).
// Accepts repeated ?path= query params plus ?scope=.
func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "me"
	}
	paths := r.URL.Query()["path"]
	if len(paths) == 0 {
		writeErr(w, http.StatusBadRequest, "no paths")
		return
	}
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// Resolve all requested paths first; abort on any invalid one.
	absSet := make([]string, 0, len(paths))
	for _, p := range paths {
		abs, err := mgr.Resolve(p)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid path")
			return
		}
		if err := mustExist(abs); err != nil {
			writeErr(w, http.StatusNotFound, "path not found")
			return
		}
		absSet = append(absSet, abs)
	}

	// Choose a zip filename from the current directory name.
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="starstack.zip"`)
	zw := zip.NewWriter(w)
	defer zw.Close()
	for _, abs := range absSet {
		if err := addZipEntry(zw, abs, filepath.Base(abs)); err != nil {
			s.log.Warn("zip entry failed", "path", abs, "err", err)
		}
	}
}

// addZipEntry writes a file or directory tree into the zip archive under `name`.
func addZipEntry(zw *zip.Writer, root, name string) error {
	fi, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return addZipFile(zw, root, name, fi)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		arc := name
		if rel != "." {
			arc = filepath.ToSlash(filepath.Join(name, rel))
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			_, err = zw.Create(arc + "/")
			return err
		}
		return addZipFile(zw, p, arc, info)
	})
}

func addZipFile(zw *zip.Writer, src, arc string, fi fs.FileInfo) error {
	hdr, err := zip.FileInfoHeader(fi)
	if err != nil {
		return err
	}
	hdr.Name = arc
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// mustExist reports whether an absolute path exists.
func mustExist(abs string) error {
	_, err := os.Stat(abs)
	return err
}

// handleDownload serves a file with Range support for resume + streaming video.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	scope, virtual, _ := s.fileParams(r)
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	abs, err := mgr.Resolve(virtual)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	file, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	name := filepath.Base(abs)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+escapeQuote(name)+"\"")
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, st.ModTime(), file)
}

// requirePreviewAuth authenticates via Authorization header OR the `auth`
// query param (needed for <img>/<video>/<iframe>, which cannot set headers).
func (s *Server) requirePreviewAuth(w http.ResponseWriter, r *http.Request) bool {
	ah := r.Header.Get("Authorization")
	tok := strings.TrimPrefix(ah, "Bearer ")
	if tok == "" || tok == ah {
		tok = r.URL.Query().Get("auth")
	}
	if tok == "" {
		writeErr(w, http.StatusUnauthorized, "missing token")
		return false
	}
	if _, err := s.auth.ParseAccessToken(tok); err != nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}

// handleRotate rotates one or more images (±90 / 180) and replaces the originals.
func (s *Server) handleRotate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope string   `json:"scope"`
		Paths []string `json:"paths"`
		Angle int      `json:"angle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	if len(req.Paths) == 0 {
		writeErr(w, http.StatusBadRequest, "no paths")
		return
	}
	if req.Angle != 90 && req.Angle != -90 && req.Angle != 180 {
		writeErr(w, http.StatusBadRequest, "angle must be 90, -90 or 180")
		return
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	results := make([]map[string]any, 0, len(req.Paths))
	for _, p := range req.Paths {
		res := map[string]any{"path": p}
		abs, err := mgr.Resolve(p)
		if err != nil {
			res["error"] = "invalid path"
			results = append(results, res)
			continue
		}
		if !preview.CanRotate(abs) {
			res["error"] = "unsupported format"
			results = append(results, res)
			continue
		}
		if err := preview.RotateOnDisk(abs, req.Angle); err != nil {
			res["error"] = err.Error()
			results = append(results, res)
			continue
		}
		res["ok"] = true
		results = append(results, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"rotated": results})
}

// handleThumb serves a generated JPEG thumbnail for an image (with disk+memory cache).
func (s *Server) handleThumb(w http.ResponseWriter, r *http.Request) {
	if !s.requirePreviewAuth(w, r) {
		return
	}
	scope, virtual, _ := s.fileParams(r)
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	abs, err := mgr.Resolve(virtual)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if !preview.SupportedImage(abs) {
		writeErr(w, http.StatusUnsupportedMediaType, "not an image")
		return
	}
	size := 0
	if sz, err := strconv.Atoi(r.URL.Query().Get("size")); err == nil {
		size = sz
	}
	quality := 0
	if q, err := strconv.Atoi(r.URL.Query().Get("q")); err == nil {
		quality = q
	}
	thumb, err := s.prev.Thumb(abs, size, quality)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(thumb)
}

// handlePreviewRaw streams a file inline (no attachment disposition) so
// browsers can embed images, audio, video and PDFs. Supports HTTP Range.
func (s *Server) handlePreviewRaw(w http.ResponseWriter, r *http.Request) {
	if !s.requirePreviewAuth(w, r) {
		return
	}
	scope, virtual, _ := s.fileParams(r)
	mgr, err := s.mgrFor(scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	abs, err := mgr.Resolve(virtual)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	file, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	name := filepath.Base(abs)
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(w, r, name, st.ModTime(), file)
}

func (s *Server) handleListShares(w http.ResponseWriter, r *http.Request) {
	shares, err := s.store.ListShares(s.currentUser(r).ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares})
}

func (s *Server) handleCreateShare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope     string `json:"scope"`
		Path      string `json:"path"`
		Password  string `json:"password"`
		ExpiresAt int64  `json:"expires_at"` // unix; 0 = never
		AllowDown bool   `json:"allow_down"`
		MaxUses   int64  `json:"max_uses"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.Scope == "" {
		req.Scope = "me"
	}
	mgr, err := s.mgrFor(req.Scope)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := mgr.Resolve(req.Path); err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	if _, err := mgr.Stat(req.Path); err != nil {
		writeErr(w, http.StatusNotFound, "path not found")
		return
	}
	sh := &store.Share{
		Token:     newToken(req.Path),
		Scope:     req.Scope,
		Path:      req.Path,
		Password:  req.Password,
		ExpiresAt: time.Unix(req.ExpiresAt, 0),
		AllowDown: req.AllowDown,
		MaxUses:   req.MaxUses,
		CreatedBy: s.currentUser(r).ID,
	}
	if err := s.store.CreateShare(sh); err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": sh.Token})
}

func (s *Server) handleDeleteShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	if err := s.store.DeleteShare(token); err != nil {
		writeErr(w, http.StatusInternalServerError, "db error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleShareToken adapts a handler so unauthenticated share links can access
// a file/dir, enforcing expiry, use limits and an optional password.
func (s *Server) handleShareToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := chi.URLParam(r, "token")
		sh, err := s.store.GetShare(token)
		if err != nil {
			writeErr(w, http.StatusNotFound, "share not found")
			return
		}
		if sh.ExpiresAt.Unix() > 0 && time.Now().After(sh.ExpiresAt) {
			writeErr(w, http.StatusGone, "share expired")
			return
		}
		if sh.MaxUses > 0 && sh.Used >= sh.MaxUses {
			writeErr(w, http.StatusGone, "share exhausted")
			return
		}
		// Optional password via ?pw= query param.
		if sh.Password != "" {
			if r.URL.Query().Get("pw") != sh.Password {
				writeErr(w, http.StatusUnauthorized, "password required")
				return
			}
		}
		_ = s.store.BumpShareUsed(sh.ID)
		// Rewrite the query so the shared scope + path are served.
		q := r.URL.Query()
		q.Set("scope", sh.Scope)
		q.Set("path", sh.Path)
		r.URL.RawQuery = q.Encode()
		next(w, r)
	}
}

func newToken(seed string) string {
	return sha256Sum(seed + strconv.FormatInt(time.Now().UnixNano(), 10))
}

func sha256Sum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func escapeQuote(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, `"`, `\"`), "\n", " ")
}
