package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/IvanZhutyaev/EventFlow/internal/auth"
	"github.com/IvanZhutyaev/EventFlow/internal/project"
)

// AdminHandler provides the bootstrap API used by the E2E flow:
// create a project and issue its first API key. Protected by ADMIN_TOKEN (§33:
// secrets via environment only).
type AdminHandler struct {
	Projects   *project.Store
	AdminToken string
}

type createProjectRequest struct {
	Email       string `json:"email"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type createProjectResponse struct {
	Project *project.Project `json:"project"`
	APIKey  string           `json:"api_key"` // shown once; only the hash is stored
}

func (h *AdminHandler) authorize(w http.ResponseWriter, r *http.Request) bool {
	reqID := RequestIDFromContext(r.Context())
	token := r.Header.Get("X-Admin-Token")
	if h.AdminToken == "" || token != h.AdminToken {
		WriteError(w, http.StatusForbidden, CodeForbidden, "admin access required", reqID)
		return false
	}
	return true
}

// CreateProject handles POST /api/v1/projects (admin token required).
// It creates the owning user if needed, the project and one API key.
func (h *AdminHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	reqID := RequestIDFromContext(r.Context())

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "invalid JSON body", reqID)
		return
	}
	if strings.TrimSpace(req.Email) == "" || strings.TrimSpace(req.Name) == "" {
		WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "email and name are required", reqID)
		return
	}

	userID, err := h.Projects.CreateUser(r.Context(), strings.TrimSpace(req.Email))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to create user", reqID)
		return
	}

	proj, err := h.Projects.CreateProject(r.Context(), userID, strings.TrimSpace(req.Name), strings.TrimSpace(req.Description))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to create project", reqID)
		return
	}

	rawKey, keyHash, err := auth.GenerateKey()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to generate API key", reqID)
		return
	}
	if _, err := h.Projects.CreateAPIKey(r.Context(), proj.ID, "default", keyHash); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeInternalError, "failed to store API key", reqID)
		return
	}

	WriteJSON(w, http.StatusCreated, createProjectResponse{Project: proj, APIKey: rawKey})
}
