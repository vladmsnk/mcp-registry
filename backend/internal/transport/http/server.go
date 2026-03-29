package http

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"mcp-registry/internal/entity"
	"mcp-registry/internal/hub"
	"mcp-registry/internal/usecase"
)

type Handler struct {
	uc       *usecase.ServerUsecase
	servers  hub.ServerRepo
	tools    hub.ToolRepo
	embedder hub.Embedder
}

func NewHandler(uc *usecase.ServerUsecase, servers hub.ServerRepo, tools hub.ToolRepo, embedder hub.Embedder) *Handler {
	return &Handler{uc: uc, servers: servers, tools: tools, embedder: embedder}
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers", h.list)
	mux.HandleFunc("POST /api/servers", h.register)
	mux.HandleFunc("POST /api/servers/{id}/sync", h.syncTools)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	servers, err := h.uc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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

	if err := h.uc.Register(r.Context(), server); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, server)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func (h *Handler) syncTools(w http.ResponseWriter, r *http.Request) {
	serverID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid server id")
		return
	}

	count, err := hub.SyncServerTools(r.Context(), h.servers, h.tools, h.embedder, serverID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"synced": count,
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
