package status

import (
	"encoding/json"
	"net/http"
	"strings"

	"emsim/internal/platform/config"
	"emsim/internal/platform/httpapi"
)

// ComponentConfigWorkerPrefix names a worker's published settings
// ("config.worker.<id>", detail {"params": [...]}).
const ComponentConfigWorkerPrefix = "config.worker."

// WithConfig sets the api's own effective settings for GET /admin/config
// (ADR-038). They are read-only: changing one means editing .env on the
// server and restarting the containers.
func (h *Handlers) WithConfig(params []config.Param) *Handlers {
	h.config = params
	return h
}

type configProcessJSON struct {
	ID      string         `json:"id"`
	Version string         `json:"version,omitempty"`
	Params  []config.Param `json:"params"`
}

type configJSON struct {
	API     configProcessJSON   `json:"api"`
	Workers []configProcessJSON `json:"workers"`
}

func (h *Handlers) getConfig(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.store.Snapshot(r.Context(), string(h.backupKind))
	if err != nil {
		httpapi.WriteError(w, r, httpapi.CodeInternalError, "failed to read configuration", nil)
		return
	}
	body := configJSON{
		API:     configProcessJSON{ID: "api", Version: h.build, Params: append([]config.Param{}, h.config...)},
		Workers: []configProcessJSON{},
	}
	versions := map[string]string{}
	for _, hb := range snapshot.Heartbeats {
		if id, ok := strings.CutPrefix(hb.Component, ComponentWorkerPrefix); ok {
			if len(id) > 40 {
				id = id[:40] // the same trim the config heartbeat id gets
			}
			versions[id], _ = hb.Detail["version"].(string)
		}
	}
	for _, hb := range snapshot.Heartbeats {
		id, ok := strings.CutPrefix(hb.Component, ComponentConfigWorkerPrefix)
		if !ok {
			continue
		}
		var detail struct {
			Params []config.Param `json:"params"`
		}
		if raw, err := json.Marshal(hb.Detail); err != nil || json.Unmarshal(raw, &detail) != nil {
			continue
		}
		if detail.Params == nil {
			detail.Params = []config.Param{}
		}
		body.Workers = append(body.Workers, configProcessJSON{ID: id, Version: versions[id], Params: detail.Params})
	}
	writeJSON(w, http.StatusOK, body)
}
