// Package notifications tells users about things that need them: a hot
// lead, a lead or task assigned to them. Each notification is stored for
// the in-app inbox and, when the sender asks, emailed too.
package notifications

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/deepashearaion/manage-crm/internal/platform/httpx"
	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

// Kinds.
const (
	KindHotLead      = "hot_lead"
	KindLeadAssigned = "lead_assigned"
	KindTaskAssigned = "task_assigned"
)

type Notification struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Link      string     `json:"link"`
	ContactID *int64     `json:"contact_id"`
	TaskID    *int64     `json:"task_id"`
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Message is a notification to send. Link is a frontend path such as
// /contacts/42; Email also emails it to each recipient.
type Message struct {
	Kind      string
	Title     string
	Body      string
	Link      string
	ContactID *int64
	TaskID    *int64
	Email     bool
}

// Directory finds users' email addresses (implemented by the auth module,
// wired in internal/app).
type Directory interface {
	Email(ctx context.Context, orgID, userID int64) (string, error)
}

var ErrNotFound = errors.New("notification not found")

type Service struct {
	db          *pgxpool.Pool
	directory   Directory
	mailer      mail.Mailer
	frontendURL string
	logger      *slog.Logger
}

func NewService(db *pgxpool.Pool, directory Directory, mailer mail.Mailer, frontendURL string, logger *slog.Logger) *Service {
	return &Service{db: db, directory: directory, mailer: mailer, frontendURL: frontendURL, logger: logger}
}

// Notify stores m for each user and emails them if m.Email. Storing is the
// part that must succeed; a failed email is logged.
func (s *Service) Notify(ctx context.Context, orgID int64, userIDs []int64, m Message) error {
	for _, uid := range userIDs {
		if _, err := s.db.Exec(ctx,
			`INSERT INTO notifications (org_id, user_id, kind, title, body, link, contact_id, task_id)
			VALUES ($1, $2, $3, left($4, 200), $5, $6, $7, $8)`,
			orgID, uid, m.Kind, m.Title, m.Body, m.Link, m.ContactID, m.TaskID,
		); err != nil {
			return fmt.Errorf("store notification for user %d: %w", uid, err)
		}
		if !m.Email {
			continue
		}
		to, err := s.directory.Email(ctx, orgID, uid)
		if err == nil {
			text := m.Body
			if m.Link != "" {
				text += "\n\nOpen in the CRM: " + s.frontendURL + m.Link
			}
			err = s.mailer.Send(ctx, mail.Message{To: to, Subject: m.Title, Text: text})
		}
		if err != nil {
			s.logger.ErrorContext(ctx, "notification email", slog.Int64("user_id", uid), slog.Any("error", err))
		}
	}
	return nil
}

const columns = `id, kind, title, body, link, contact_id, task_id, read_at, created_at`

// List returns p's notifications, newest first, and how many are unread.
func (s *Service) List(ctx context.Context, p principal.Principal, unreadOnly bool, limit int, beforeID int64) ([]Notification, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+columns+` FROM notifications
		WHERE org_id = $1 AND user_id = $2 AND (NOT $3 OR read_at IS NULL) AND ($4::bigint = 0 OR id < $4)
		ORDER BY created_at DESC, id DESC LIMIT $5`,
		p.OrgID, p.UserID, unreadOnly, beforeID, limit,
	)
	if err != nil {
		return nil, 0, err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Notification, error) {
		var n Notification
		err := row.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Link, &n.ContactID, &n.TaskID, &n.ReadAt, &n.CreatedAt)
		return n, err
	})
	if err != nil {
		return nil, 0, err
	}
	var unread int
	err = s.db.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE org_id = $1 AND user_id = $2 AND read_at IS NULL`,
		p.OrgID, p.UserID,
	).Scan(&unread)
	return list, unread, err
}

// MarkRead marks one of p's notifications read.
func (s *Service) MarkRead(ctx context.Context, p principal.Principal, id int64) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = COALESCE(read_at, now()) WHERE org_id = $1 AND user_id = $2 AND id = $3`,
		p.OrgID, p.UserID, id,
	)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// MarkAllRead marks all of p's notifications read.
func (s *Service) MarkAllRead(ctx context.Context, p principal.Principal) error {
	_, err := s.db.Exec(ctx,
		`UPDATE notifications SET read_at = now() WHERE org_id = $1 AND user_id = $2 AND read_at IS NULL`,
		p.OrgID, p.UserID,
	)
	return err
}

// HTTP

type Handler struct {
	svc    *Service
	logger *slog.Logger
}

func NewHandler(svc *Service, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

// List handles GET /api/v1/notifications?unread=true&limit=&before_id=.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	q := r.URL.Query()
	limit, err1 := optionalInt(q.Get("limit"))
	before, err2 := optionalInt(q.Get("before_id"))
	if err1 != nil || err2 != nil {
		httpx.WriteProblem(w, r, http.StatusBadRequest, "invalid_query", "limit and before_id must be positive integers.")
		return
	}
	list, unread, err := h.svc.List(r.Context(), p, q.Get("unread") == "true", int(limit), before)
	if err != nil {
		h.internal(w, r, "list notifications", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"notifications": list, "unread_count": unread})
}

// MarkRead handles POST /api/v1/notifications/{id}/read.
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	id, err := strconv.ParseInt(mux.Vars(r)["id"], 10, 64)
	if err == nil {
		err = h.svc.MarkRead(r.Context(), p, id)
	} else {
		err = ErrNotFound
	}
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteProblem(w, r, http.StatusNotFound, "not_found", "No such notification.")
	case err != nil:
		h.internal(w, r, "mark notification read", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// MarkAllRead handles POST /api/v1/notifications/read-all.
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	p, _ := principal.From(r.Context())
	if err := h.svc.MarkAllRead(r.Context(), p); err != nil {
		h.internal(w, r, "mark all notifications read", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) internal(w http.ResponseWriter, r *http.Request, action string, err error) {
	h.logger.ErrorContext(r.Context(), action, slog.Any("error", err))
	httpx.WriteProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong.")
}

func optionalInt(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err == nil && n <= 0 {
		err = errors.New("not positive")
	}
	return n, err
}

// Routes registers the notification endpoints; each user sees only their own.
func Routes(r *mux.Router, h *Handler, requireUserV1 func(http.Handler) http.Handler) {
	r.Handle("/api/v1/notifications", requireUserV1(http.HandlerFunc(h.List))).Methods(http.MethodGet)
	r.Handle("/api/v1/notifications/read-all", requireUserV1(http.HandlerFunc(h.MarkAllRead))).Methods(http.MethodPost)
	r.Handle("/api/v1/notifications/{id}/read", requireUserV1(http.HandlerFunc(h.MarkRead))).Methods(http.MethodPost)
}
