package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"mcp-registry/internal/audit"
	"mcp-registry/internal/auth"
	"mcp-registry/internal/entity"
	"mcp-registry/internal/health"
	"mcp-registry/internal/hub"
	"mcp-registry/internal/repository"
	"mcp-registry/internal/security"
	"mcp-registry/internal/usecase"
)

const (
	RoleUser           = "mcp-user"
	RoleAdmin          = "mcp-admin"
	ActionToolSetRoles = "tool.set_roles"
)

// ToolAdminRepo is a small admin-only interface for managing tool RBAC.
type ToolAdminRepo interface {
	SetRequiredRoles(ctx context.Context, serverID int64, toolName string, roles []string) error
}

// HealthProber runs an on-demand health check against a target and persists the result.
type HealthProber interface {
	CheckOne(ctx context.Context, t repository.HealthTarget) health.Status
}

// HealthReader reads the persisted health status for a server.
type HealthReader interface {
	Get(ctx context.Context, serverID int64) (health.Status, error)
}

type Handler struct {
	uc            *usecase.ServerUsecase
	servers       hub.ServerRepo
	tools         hub.ToolRepo
	toolAdmin     ToolAdminRepo
	embedder      hub.Embedder
	audit         *audit.Logger
	healthChecker HealthProber
	healthReader  HealthReader
	httpOpts      security.ClientOptions
}

func NewHandler(
	uc *usecase.ServerUsecase,
	servers hub.ServerRepo,
	tools hub.ToolRepo,
	toolAdmin ToolAdminRepo,
	embedder hub.Embedder,
	auditLog *audit.Logger,
	healthChecker HealthProber,
	healthReader HealthReader,
	httpOpts security.ClientOptions,
) *Handler {
	return &Handler{
		uc: uc, servers: servers, tools: tools, toolAdmin: toolAdmin,
		embedder: embedder, audit: auditLog,
		healthChecker: healthChecker, healthReader: healthReader,
		httpOpts: httpOpts,
	}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/servers", auth.RequireRole(h.audit, RoleUser, RoleAdmin)(http.HandlerFunc(h.list)))
	mux.Handle("POST /api/servers", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.register)))
	mux.Handle("DELETE /api/servers/{id}", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.delete)))
	mux.Handle("POST /api/servers/{id}/sync", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.syncTools)))
	mux.Handle("PUT /api/servers/{id}/tools/{name}/roles", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.setToolRoles)))
	mux.Handle("GET /api/servers/{id}/health", auth.RequireRole(h.audit, RoleUser, RoleAdmin)(http.HandlerFunc(h.getHealth)))
	mux.Handle("POST /api/servers/{id}/health", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.probeHealth)))
	mux.Handle("POST /api/servers/{id}/repin", auth.RequireRole(h.audit, RoleAdmin)(http.HandlerFunc(h.repinTLS)))
}

func (h *Handler) newEvent(r *http.Request, action string, started time.Time) audit.Event {
	claims := auth.ClaimsFromContext(r.Context())
	e := audit.Event{
		Action:    action,
		IP:        audit.ClientIP(r),
		UserAgent: r.Header.Get("User-Agent"),
		RequestID: r.Header.Get("X-Request-ID"),
		LatencyMS: time.Since(started).Milliseconds(),
	}
	if claims != nil {
		e.ActorSub = claims.Subject
		e.ActorUsername = claims.PreferredUsername
		e.ActorRoles = claims.RealmRoles
	}
	return e
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	servers, err := h.uc.List(r.Context())

	ev := h.newEvent(r, audit.ActionServerList, started)
	if err != nil {
		ev.Status = audit.StatusError
		ev.Error = err.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	ev.Metadata = map[string]any{"count": len(servers)}
	h.audit.Log(r.Context(), ev)
	writeJSON(w, http.StatusOK, servers)
}

type registerRequest struct {
	Name        string   `json:"name"`
	Endpoint    string   `json:"endpoint"`
	Description string   `json:"description"`
	Owner       string   `json:"owner"`
	AuthType    string   `json:"authType"`
	Tags        []string `json:"tags"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ev := h.newEvent(r, audit.ActionServerRegister, started)
		ev.Status = audit.StatusError
		ev.Error = "invalid request body"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	server := &entity.Server{
		Name:        req.Name,
		Endpoint:    req.Endpoint,
		Description: req.Description,
		Owner:       req.Owner,
		AuthType:    req.AuthType,
		Tags:        req.Tags,
	}

	err := h.uc.Register(r.Context(), server)

	ev := h.newEvent(r, audit.ActionServerRegister, started)
	ev.Metadata = map[string]any{"name": req.Name, "endpoint": req.Endpoint}
	if err != nil {
		ev.Status = audit.StatusError
		ev.Error = err.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	ev.ServerID = server.ID
	h.audit.Log(r.Context(), ev)

	writeJSON(w, http.StatusCreated, server)
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		ev := h.newEvent(r, audit.ActionServerDelete, started)
		ev.Status = audit.StatusError
		ev.Error = "invalid server id"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	delErr := h.uc.Delete(r.Context(), serverID)

	ev := h.newEvent(r, audit.ActionServerDelete, started)
	ev.ServerID = serverID
	if delErr != nil {
		ev.Status = audit.StatusError
		ev.Error = delErr.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusInternalServerError, delErr.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	h.audit.Log(r.Context(), ev)

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) syncTools(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		ev := h.newEvent(r, audit.ActionServerSync, started)
		ev.Status = audit.StatusError
		ev.Error = "invalid server id"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	count, syncErr := hub.SyncServerTools(r.Context(), h.servers, h.tools, h.embedder, h.httpOpts, serverID)

	ev := h.newEvent(r, audit.ActionServerSync, started)
	ev.ServerID = serverID
	if syncErr != nil {
		ev.Status = audit.StatusError
		ev.Error = syncErr.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadGateway, syncErr.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	ev.Metadata = map[string]any{"synced": count}
	h.audit.Log(r.Context(), ev)

	writeJSON(w, http.StatusOK, map[string]any{
		"synced": count,
	})
}

type setToolRolesRequest struct {
	RequiredRoles []string `json:"required_roles"`
}

func (h *Handler) setToolRoles(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		ev := h.newEvent(r, ActionToolSetRoles, started)
		ev.Status = audit.StatusError
		ev.Error = "invalid server id"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}
	toolName := r.PathValue("name")

	var req setToolRolesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		ev := h.newEvent(r, ActionToolSetRoles, started)
		ev.ServerID = serverID
		ev.ToolName = toolName
		ev.Status = audit.StatusError
		ev.Error = "invalid request body"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updateErr := h.toolAdmin.SetRequiredRoles(r.Context(), serverID, toolName, req.RequiredRoles)

	ev := h.newEvent(r, ActionToolSetRoles, started)
	ev.ServerID = serverID
	ev.ToolName = toolName
	ev.Metadata = map[string]any{"required_roles": req.RequiredRoles}

	if errors.Is(updateErr, sql.ErrNoRows) {
		ev.Status = audit.StatusError
		ev.Error = "tool not found"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusNotFound, "tool not found")
		return
	}
	if updateErr != nil {
		ev.Status = audit.StatusError
		ev.Error = updateErr.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusInternalServerError, updateErr.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	h.audit.Log(r.Context(), ev)

	writeJSON(w, http.StatusOK, map[string]any{
		"server_id":      serverID,
		"tool_name":      toolName,
		"required_roles": req.RequiredRoles,
	})
}

func (h *Handler) getHealth(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	status, err := h.healthReader.Get(r.Context(), serverID)
	if errors.Is(err, sql.ErrNoRows) {
		// Never probed yet — return zero-valued status so callers always get a usable shape.
		writeJSON(w, http.StatusOK, status)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (h *Handler) repinTLS(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	oldPin, newPin, repinErr := h.uc.RepinTLS(r.Context(), serverID)

	ev := h.newEvent(r, "server.repin", started)
	ev.ServerID = serverID
	if repinErr != nil {
		ev.Status = audit.StatusError
		ev.Error = repinErr.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadGateway, repinErr.Error())
		return
	}
	ev.Status = audit.StatusAllowed
	ev.Metadata = map[string]any{"old_pin": oldPin, "new_pin": newPin, "changed": oldPin != newPin}
	h.audit.Log(r.Context(), ev)

	writeJSON(w, http.StatusOK, map[string]any{
		"server_id": serverID,
		"old_pin":   oldPin,
		"new_pin":   newPin,
		"changed":   oldPin != newPin,
	})
}

func (h *Handler) probeHealth(w http.ResponseWriter, r *http.Request) {
	started := time.Now()

	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		ev := h.newEvent(r, audit.ActionServerHealth, started)
		ev.Status = audit.StatusError
		ev.Error = "invalid server id"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	endpoint, name, _, _, active, lookupErr := h.servers.GetEndpoint(r.Context(), serverID)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		ev := h.newEvent(r, audit.ActionServerHealth, started)
		ev.ServerID = serverID
		ev.Status = audit.StatusError
		ev.Error = "server not found"
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if lookupErr != nil {
		ev := h.newEvent(r, audit.ActionServerHealth, started)
		ev.ServerID = serverID
		ev.Status = audit.StatusError
		ev.Error = lookupErr.Error()
		h.audit.Log(r.Context(), ev)
		writeError(w, http.StatusInternalServerError, lookupErr.Error())
		return
	}

	target := repository.HealthTarget{ID: serverID, Name: name, Endpoint: endpoint, Active: active}
	probeStatus := h.healthChecker.CheckOne(r.Context(), target)

	persisted, _ := h.healthReader.Get(r.Context(), serverID)

	ev := h.newEvent(r, audit.ActionServerHealth, started)
	ev.ServerID = serverID
	if probeStatus.LastError != "" {
		ev.Status = audit.StatusError
		ev.Error = probeStatus.LastError
	} else {
		ev.Status = audit.StatusAllowed
	}
	h.audit.Log(r.Context(), ev)

	writeJSON(w, http.StatusOK, persisted)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
