package leads

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// WhatsAppSettings configures the inbound webhook; empty disables it.
type WhatsAppSettings struct {
	AppSecret   string
	VerifyToken string
}

type Handler struct {
	svc      *Service
	logger   *slog.Logger
	whatsapp WhatsAppSettings
}

func NewHandler(svc *Service, logger *slog.Logger, wa WhatsAppSettings) *Handler {
	return &Handler{svc: svc, logger: logger, whatsapp: wa}
}

// leadRequest is the body of POST /api/v1/leads.
type leadRequest struct {
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Email       string `json:"email"`
	Mobile      string `json:"mobile"`
	Destination string `json:"destination"`
	Source      string `json:"source"`
	Notes       string `json:"notes"`
	Message     string `json:"message"`
	Channel     string `json:"channel"`
}

// CreateLead handles POST /api/v1/leads.
func (h *Handler) CreateLead(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var req leadRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	res, err := h.svc.CaptureByUser(r.Context(), p, Lead{
		FirstName: req.FirstName, LastName: req.LastName, Email: req.Email, Mobile: req.Mobile,
		Destination: req.Destination, Source: req.Source, Notes: req.Notes, Message: req.Message,
		Channel: req.Channel,
	})
	if err != nil {
		h.writeError(w, r, "capture lead", err)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"lead": res})
}

// Rescore handles POST /api/v1/leads/{id}/rescore.
func (h *Handler) Rescore(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := h.svc.Rescore(r.Context(), p, id)
	if err != nil {
		h.writeError(w, r, "rescore lead", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"lead": res})
}

// SubmitForm handles POST /api/v1/intake/forms/{key}: a public web form or
// integration. It accepts JSON or an HTML form post, ignores fields it does
// not know (so existing forms can post as they are), and answers 202
// without revealing whether the person was already known. A filled-in
// "website" field is the honeypot: such submissions are dropped silently.
func (h *Handler) SubmitForm(w http.ResponseWriter, r *http.Request) {
	fields, err := formFields(w, r)
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_body", "Send JSON or a form post.")
		return
	}
	if fields["website"] != "" {
		httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
		return
	}
	first, last := fields["first_name"], fields["last_name"]
	if first == "" && fields["name"] != "" {
		first, last, _ = strings.Cut(strings.Join(strings.Fields(fields["name"]), " "), " ")
	}
	mobile := fields["mobile"]
	if mobile == "" {
		mobile = fields["phone"]
	}
	_, err = h.svc.SubmitForm(r.Context(), mux.Vars(r)["key"], Lead{
		FirstName: first, LastName: last, Email: fields["email"], Mobile: mobile,
		Destination: fields["destination"], Message: fields["message"],
	})
	if errors.Is(err, ErrSourceInactive) {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such form.")
		return
	}
	if err != nil {
		h.writeError(w, r, "submit form", err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]string{"status": "received"})
}

// maxFormBytes caps a form submission; a lead form is a few hundred bytes.
const maxFormBytes = 64 << 10

var formKeys = []string{"first_name", "last_name", "name", "email", "mobile", "phone", "destination", "message", "website"}

// formFields reads the known fields of a JSON or url-encoded form body.
func formFields(w http.ResponseWriter, r *http.Request) (map[string]string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	out := map[string]string{}
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			return nil, err
		}
		for _, k := range formKeys {
			switch v := raw[k].(type) {
			case string:
				out[k] = strings.TrimSpace(v)
			case float64:
				out[k] = strconv.FormatFloat(v, 'f', -1, 64)
			}
		}
		return out, nil
	}
	// HTML forms post application/x-www-form-urlencoded by default; lead
	// forms have no file uploads, so multipart bodies are not accepted.
	if ct != "application/x-www-form-urlencoded" {
		return nil, errors.New("unsupported content type " + ct)
	}
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	for _, k := range formKeys {
		out[k] = strings.TrimSpace(r.PostForm.Get(k))
	}
	return out, nil
}

// VerifyWhatsApp handles GET /api/v1/webhooks/whatsapp, Meta's subscription
// handshake: echo hub.challenge when hub.verify_token matches.
func (h *Handler) VerifyWhatsApp(w http.ResponseWriter, r *http.Request) {
	if h.whatsapp.AppSecret == "" {
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "whatsapp_disabled", "WhatsApp is not configured.")
		return
	}
	q := r.URL.Query()
	if q.Get("hub.mode") != "subscribe" || !validVerifyToken(h.whatsapp.VerifyToken, q.Get("hub.verify_token")) {
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Verification failed.")
		return
	}
	// Meta's challenge is a number; echo only that, never arbitrary input.
	challenge, err := strconv.ParseUint(q.Get("hub.challenge"), 10, 64)
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "hub.challenge must be a number.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, strconv.FormatUint(challenge, 10))
}

// WhatsAppWebhook handles POST /api/v1/webhooks/whatsapp. Unsigned or
// wrongly signed bodies are rejected; a 500 makes Meta retry, which is safe
// because captures are idempotent by message id.
func (h *Handler) WhatsAppWebhook(w http.ResponseWriter, r *http.Request) {
	if h.whatsapp.AppSecret == "" {
		httpx.WriteProblem(w, r, http.StatusServiceUnavailable, "whatsapp_disabled", "WhatsApp is not configured.")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_body", "Could not read the body.")
		return
	}
	if !validSignature(h.whatsapp.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		httpx.WriteProblem(w, r, http.StatusUnauthorized, "invalid_signature", "Invalid or missing X-Hub-Signature-256.")
		return
	}
	if err := h.svc.HandleWhatsAppMessages(r.Context(), body); err != nil {
		h.logger.ErrorContext(r.Context(), "whatsapp webhook", slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// Configuration endpoints.

func (h *Handler) GetSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	s, err := h.svc.Settings(r.Context(), p)
	h.respond(w, r, "get lead settings", http.StatusOK, "settings", s, err)
}

func (h *Handler) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var patch SettingsPatch
	if !httpx.DecodeJSON(w, r, &patch) {
		return
	}
	s, err := h.svc.UpdateSettings(r.Context(), p, patch)
	h.respond(w, r, "update lead settings", http.StatusOK, "settings", s, err)
}

func (h *Handler) ListSources(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.Sources(r.Context(), p)
	h.respond(w, r, "list lead sources", http.StatusOK, "sources", list, err)
}

func (h *Handler) CreateSource(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var req struct {
		Name    string `json:"name"`
		Channel string `json:"channel"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, err := h.svc.CreateSource(r.Context(), p, req.Name, req.Channel)
	h.respond(w, r, "create lead source", http.StatusCreated, "source", s, err)
}

func (h *Handler) UpdateSource(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Name   *string `json:"name"`
		Active *bool   `json:"active"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	s, err := h.svc.UpdateSource(r.Context(), p, id, req.Name, req.Active, false)
	h.respond(w, r, "update lead source", http.StatusOK, "source", s, err)
}

func (h *Handler) RotateSourceKey(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s, err := h.svc.UpdateSource(r.Context(), p, id, nil, nil, true)
	h.respond(w, r, "rotate lead source key", http.StatusOK, "source", s, err)
}

func (h *Handler) ListWhatsAppAccounts(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.WhatsAppAccounts(r.Context(), p)
	h.respond(w, r, "list whatsapp accounts", http.StatusOK, "accounts", list, err)
}

func (h *Handler) CreateWhatsAppAccount(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	var req struct {
		PhoneNumberID string `json:"phone_number_id"`
		DisplayPhone  string `json:"display_phone"`
		SourceName    string `json:"source_name"`
	}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	a, err := h.svc.CreateWhatsAppAccount(r.Context(), p, WhatsAppAccount{
		PhoneNumberID: req.PhoneNumberID, DisplayPhone: req.DisplayPhone, SourceName: req.SourceName,
	})
	h.respond(w, r, "create whatsapp account", http.StatusCreated, "account", a, err)
}

func (h *Handler) DeleteWhatsAppAccount(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.noContent(w, r, "delete whatsapp account", h.svc.DeleteWhatsAppAccount(r.Context(), p, id))
}

func (h *Handler) ListScoringRules(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.ScoringRules(r.Context(), p)
	h.respond(w, r, "list scoring rules", http.StatusOK, "rules", list, err)
}

func (h *Handler) CreateScoringRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	req := struct {
		Name      string `json:"name"`
		Criterion string `json:"criterion"`
		Value     string `json:"value"`
		Points    int    `json:"points"`
		Active    *bool  `json:"active"`
	}{}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	rule := ScoringRule{Name: req.Name, Criterion: req.Criterion, Value: req.Value, Points: req.Points, Active: true}
	apply(&rule.Active, req.Active)
	s, err := h.svc.CreateScoringRule(r.Context(), p, rule)
	h.respond(w, r, "create scoring rule", http.StatusCreated, "rule", s, err)
}

func (h *Handler) UpdateScoringRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var patch ScoringRulePatch
	if !httpx.DecodeJSON(w, r, &patch) {
		return
	}
	s, err := h.svc.UpdateScoringRule(r.Context(), p, id, patch)
	h.respond(w, r, "update scoring rule", http.StatusOK, "rule", s, err)
}

func (h *Handler) DeleteScoringRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.noContent(w, r, "delete scoring rule", h.svc.DeleteScoringRule(r.Context(), p, id))
}

func (h *Handler) ListAssignmentRules(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	list, err := h.svc.AssignmentRules(r.Context(), p)
	h.respond(w, r, "list assignment rules", http.StatusOK, "rules", list, err)
}

func (h *Handler) CreateAssignmentRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	req := struct {
		Name      string  `json:"name"`
		Position  int     `json:"position"`
		Criterion string  `json:"criterion"`
		Value     string  `json:"value"`
		Active    *bool   `json:"active"`
		UserIDs   []int64 `json:"user_ids"`
	}{}
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	rule := AssignmentRule{Name: req.Name, Position: req.Position, Criterion: req.Criterion,
		Value: req.Value, Active: true, UserIDs: req.UserIDs}
	apply(&rule.Active, req.Active)
	a, err := h.svc.CreateAssignmentRule(r.Context(), p, rule)
	h.respond(w, r, "create assignment rule", http.StatusCreated, "rule", a, err)
}

func (h *Handler) UpdateAssignmentRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var patch AssignmentRulePatch
	if !httpx.DecodeJSON(w, r, &patch) {
		return
	}
	a, err := h.svc.UpdateAssignmentRule(r.Context(), p, id, patch)
	h.respond(w, r, "update assignment rule", http.StatusOK, "rule", a, err)
}

func (h *Handler) DeleteAssignmentRule(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	h.noContent(w, r, "delete assignment rule", h.svc.DeleteAssignmentRule(r.Context(), p, id))
}

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, action string, status int, key string, v any, err error) {
	if err != nil {
		h.writeError(w, r, action, err)
		return
	}
	httpx.WriteJSON(w, status, map[string]any{key: v})
}

func (h *Handler) noContent(w http.ResponseWriter, r *http.Request, action string, err error) {
	if err != nil {
		h.writeError(w, r, action, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeError answers a service error as problem+json.
func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, action string, err error) {
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		httpx.WriteProblemFields(w, r, http.StatusUnprocessableEntity, "validation_failed", fe.Message,
			map[string]string{fe.Field: fe.Problem})
	case errors.Is(err, ErrForbidden):
		httpx.WriteProblem(w, r, http.StatusForbidden, "forbidden", "Your role does not allow this.")
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, ErrContactNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such contact.")
	case errors.Is(err, ErrDuplicateContact):
		httpx.WriteProblem(w, r, http.StatusConflict, "duplicate_contact",
			"A contact with this email or mobile already exists and is owned by someone else.")
	case errors.Is(err, ErrInvalidOwner):
		httpx.WriteProblem(w, r, http.StatusUnprocessableEntity, "invalid_owner", "That user is not in your organization.")
	case errors.Is(err, ErrNameTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "name_taken", "That name is already used.",
			map[string]string{"name": "taken"})
	case errors.Is(err, ErrPhoneNumberTaken):
		httpx.WriteProblemFields(w, r, http.StatusConflict, "phone_number_taken", "That WhatsApp number is already registered.",
			map[string]string{"phone_number_id": "taken"})
	default:
		h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
		httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}
