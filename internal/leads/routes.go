package leads

import (
	"net/http"

	"github.com/gorilla/mux"
)

// Routes registers the lead endpoints. Intake forms and the WhatsApp
// webhook are public (the form key and the webhook signature identify the
// sender); everything else needs a signed-in user (problem+json 401s).
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	r.HandleFunc("/api/v1/intake/forms/{key}", h.SubmitForm).Methods(http.MethodPost)
	r.HandleFunc("/api/v1/webhooks/whatsapp", h.VerifyWhatsApp).Methods(http.MethodGet)
	r.HandleFunc("/api/v1/webhooks/whatsapp", h.WhatsAppWebhook).Methods(http.MethodPost)

	v1 := func(path, method string, f http.HandlerFunc) {
		r.Handle(path, requireUserV1(f)).Methods(method)
	}
	v1("/api/v1/leads", http.MethodPost, h.CreateLead)
	v1("/api/v1/leads/{id}/rescore", http.MethodPost, h.Rescore)

	v1("/api/v1/lead-settings", http.MethodGet, h.GetSettings)
	v1("/api/v1/lead-settings", http.MethodPatch, h.UpdateSettings)

	v1("/api/v1/lead-sources", http.MethodGet, h.ListSources)
	v1("/api/v1/lead-sources", http.MethodPost, h.CreateSource)
	v1("/api/v1/lead-sources/{id}", http.MethodPatch, h.UpdateSource)
	v1("/api/v1/lead-sources/{id}/rotate-key", http.MethodPost, h.RotateSourceKey)

	v1("/api/v1/whatsapp-accounts", http.MethodGet, h.ListWhatsAppAccounts)
	v1("/api/v1/whatsapp-accounts", http.MethodPost, h.CreateWhatsAppAccount)
	v1("/api/v1/whatsapp-accounts/{id}", http.MethodDelete, h.DeleteWhatsAppAccount)

	v1("/api/v1/scoring-rules", http.MethodGet, h.ListScoringRules)
	v1("/api/v1/scoring-rules", http.MethodPost, h.CreateScoringRule)
	v1("/api/v1/scoring-rules/{id}", http.MethodPatch, h.UpdateScoringRule)
	v1("/api/v1/scoring-rules/{id}", http.MethodDelete, h.DeleteScoringRule)

	v1("/api/v1/assignment-rules", http.MethodGet, h.ListAssignmentRules)
	v1("/api/v1/assignment-rules", http.MethodPost, h.CreateAssignmentRule)
	v1("/api/v1/assignment-rules/{id}", http.MethodPatch, h.UpdateAssignmentRule)
	v1("/api/v1/assignment-rules/{id}", http.MethodDelete, h.DeleteAssignmentRule)
}
