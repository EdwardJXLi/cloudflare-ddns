package hub

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"

	"cloudflare-ddns/internal/cloudflare"
	"cloudflare-ddns/internal/store"
)

type DNSUpdater interface {
	Upsert(context.Context, string, cloudflare.Record) (cloudflare.Result, error)
}

type Server struct {
	store      *store.Store
	cloudflare DNSUpdater
	logger     *slog.Logger
	zoneID     string
	zone       string
}

type updateRequest struct {
	Address string `json:"address"`
}

type recordResult struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

func New(credentials *store.Store, updater DNSUpdater, zoneID, zone string, logger *slog.Logger) *Server {
	return &Server{
		store: credentials, cloudflare: updater, zoneID: zoneID, zone: zone, logger: logger,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /v1/update", s.update)

	return securityHeaders(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r.Header.Get("Authorization"))
	if !ok {
		unauthorized(w)
		return
	}

	credential, err := s.store.Authenticate(token)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.logger.Error("credential database error", "error", err)
		}
		unauthorized(w)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request updateRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON request")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "request must contain exactly one JSON object")
		return
	}

	if err := validateAddress(request.Address); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	recordName := credential.ID + "." + s.zone
	result, err := s.cloudflare.Upsert(r.Context(), s.zoneID, cloudflare.Record{
		Type: "A", Name: recordName, Content: request.Address, TTL: cloudflare.AutomaticTTL,
	})
	if err != nil {
		s.logger.Error("DNS update failed", "client", credential.ID, "record", recordName, "error", err)
		writeError(w, http.StatusBadGateway, "Cloudflare update failed")
		return
	}

	status := "unchanged"
	if result.Created {
		status = "created"
	} else if result.Changed {
		status = "updated"
	}

	resultBody := recordResult{Name: recordName, Type: "A", Address: request.Address, Status: status}

	s.logger.Info("DNS update complete", "client", credential.ID, "record", recordName, "status", status)
	writeJSON(w, http.StatusOK, map[string]any{"client": credential.ID, "result": resultBody})
}

func validateAddress(raw string) error {
	ip, err := netip.ParseAddr(raw)
	if err != nil || !ip.Is4() {
		return errors.New("address must be a valid, unmapped IPv4 address")
	}
	if !ip.IsGlobalUnicast() || ip.IsPrivate() {
		return errors.New("address must be a public IPv4 address")
	}

	return nil
}

func bearerToken(header string) (string, bool) {
	scheme, token, found := strings.Cut(header, " ")
	return token, found && strings.EqualFold(scheme, "Bearer") && token != "" && !strings.ContainsAny(token, " \t\r\n")
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "invalid credentials")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value) //nolint:errcheck
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
