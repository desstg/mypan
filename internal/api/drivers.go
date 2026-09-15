package api

import (
	"net/http"

	"litepan/internal/driver"
)

func (h *Handler) listDrivers(w http.ResponseWriter, _ *http.Request) {
	writeOK(w, driver.List())
}
