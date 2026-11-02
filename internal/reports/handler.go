package reports

import (
	"log/slog"
	"net/http"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) SalesDashboard(w http.ResponseWriter, r *http.Request) {
	p, ok := principal.From(r.Context())
	if !ok {
		http.Error(w, "User authentication information not found", http.StatusUnauthorized)
		return
	}

	d, err := h.svc.SalesDashboard(r.Context(), p)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "sales dashboard", slog.Any("error", err))
		http.Error(w, "Failed to load sales dashboard", http.StatusInternalServerError)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}
