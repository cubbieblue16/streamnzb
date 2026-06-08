package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"streamnzb/pkg/auth"
	"streamnzb/pkg/core/config"
	"streamnzb/pkg/core/logger"
	"streamnzb/pkg/services/notifier"
)

// handleNotifierTest delivers a synthetic alert to a single channel config so the
// settings UI can validate a channel before saving. The request body is one
// NotifierChannelConfig (type/url/header/priority). Delivery failures return
// HTTP 200 with {"success": false, "error": ...} so the frontend can surface the
// provider's error text directly rather than parsing a non-2xx response.
func (s *Server) handleNotifierTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	stream, _ := auth.StreamFromContext(r)
	if stream == nil || stream.Username != s.config.GetAdminUsername() {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var cc config.NotifierChannelConfig
	if err := json.NewDecoder(r.Body).Decode(&cc); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	if err := notifier.SendTest(ctx, cc, nil); err != nil {
		logger.Warn("Notifier test send failed", "type", cc.Type, "name", cc.Name, "err", err)
		json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
}
