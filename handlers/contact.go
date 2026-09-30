package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"authentication-backend/middleware"
	"authentication-backend/models"

	"github.com/jackc/pgx/v5"
)

type ContactHandler struct {
	DB *pgx.Conn
}

// Create a new contact
func (h *ContactHandler) CreateContact(w http.ResponseWriter, r *http.Request) {

	// Get logged-in user's ID from JWT middleware
	userID, ok := r.Context().Value(middleware.UserIDKey).(int64)

	if !ok {
		http.Error(
			w,
			"User authentication information not found",
			http.StatusUnauthorized,
		)
		return
	}

	// Decode request body
	var contact models.Contact

	err := json.NewDecoder(r.Body).Decode(&contact)

	if err != nil {
		http.Error(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	// Validate required field
	if contact.FirstName == "" {
		http.Error(
			w,
			"First name is required",
			http.StatusBadRequest,
		)
		return
	}

	// Set logged-in user as lead owner
	contact.LeadOwnerID = &userID

	query := `
		INSERT INTO contacts (
			first_name,
			last_name,
			email,
			mobile,
			alternate_mobile,
			company_id,
			lead_status_id,
			lead_owner_id,
			destination,
			source,
			notes
		)
		VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9, $10, $11
		)
		RETURNING id, created_at, updated_at
	`

	err = h.DB.QueryRow(
		r.Context(),
		query,
		contact.FirstName,
		contact.LastName,
		contact.Email,
		contact.Mobile,
		contact.AlternateMobile,
		contact.CompanyID,
		contact.LeadStatusID,
		contact.LeadOwnerID,
		contact.Destination,
		contact.Source,
		contact.Notes,
	).Scan(
		&contact.ID,
		&contact.CreatedAt,
		&contact.UpdatedAt,
	)

	if err != nil {
		http.Error(
			w,
			"Failed to create contact",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Contact created successfully",
		"contact": contact,
	})
}

// Get all contacts
func (h *ContactHandler) GetContacts(w http.ResponseWriter, r *http.Request) {

	query := `
		SELECT
			id,
			first_name,
			COALESCE(last_name, ''),
			COALESCE(email, ''),
			COALESCE(mobile, ''),
			COALESCE(alternate_mobile, ''),
			company_id,
			lead_status_id,
			lead_owner_id,
			COALESCE(destination, ''),
			COALESCE(source, ''),
			COALESCE(notes, ''),
			created_at,
			updated_at
		FROM contacts
		ORDER BY id DESC
	`

	rows, err := h.DB.Query(r.Context(), query)

	if err != nil {
		http.Error(
			w,
			"Failed to fetch contacts",
			http.StatusInternalServerError,
		)
		return
	}
	defer rows.Close()

	// Initialize as an empty array instead of null
	contacts := make([]models.Contact, 0)

	for rows.Next() {

		var contact models.Contact

		err := rows.Scan(
			&contact.ID,
			&contact.FirstName,
			&contact.LastName,
			&contact.Email,
			&contact.Mobile,
			&contact.AlternateMobile,
			&contact.CompanyID,
			&contact.LeadStatusID,
			&contact.LeadOwnerID,
			&contact.Destination,
			&contact.Source,
			&contact.Notes,
			&contact.CreatedAt,
			&contact.UpdatedAt,
		)

		if err != nil {
			http.Error(
				w,
				"Failed to read contact data",
				http.StatusInternalServerError,
			)
			return
		}

		contacts = append(contacts, contact)
	}

	if err := rows.Err(); err != nil {
		http.Error(
			w,
			"Error while fetching contacts",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"contacts": contacts,
	})
}

// Get a contact by ID
func (h *ContactHandler) GetContactByID(
	w http.ResponseWriter,
	r *http.Request,
) {

	// Get contact ID from URL
	idString := strings.TrimPrefix(
		r.URL.Path,
		"/api/contacts/",
	)

	// Validate contact ID
	contactID, err := strconv.ParseInt(idString, 10, 64)

	if err != nil || contactID <= 0 {
		http.Error(
			w,
			"Invalid contact ID",
			http.StatusBadRequest,
		)
		return
	}

	query := `
		SELECT
			id,
			first_name,
			COALESCE(last_name, ''),
			COALESCE(email, ''),
			COALESCE(mobile, ''),
			COALESCE(alternate_mobile, ''),
			company_id,
			lead_status_id,
			lead_owner_id,
			COALESCE(destination, ''),
			COALESCE(source, ''),
			COALESCE(notes, ''),
			created_at,
			updated_at
		FROM contacts
		WHERE id = $1
	`

	var contact models.Contact

	err = h.DB.QueryRow(
		r.Context(),
		query,
		contactID,
	).Scan(
		&contact.ID,
		&contact.FirstName,
		&contact.LastName,
		&contact.Email,
		&contact.Mobile,
		&contact.AlternateMobile,
		&contact.CompanyID,
		&contact.LeadStatusID,
		&contact.LeadOwnerID,
		&contact.Destination,
		&contact.Source,
		&contact.Notes,
		&contact.CreatedAt,
		&contact.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			http.Error(
				w,
				"Contact not found",
				http.StatusNotFound,
			)
			return
		}

		http.Error(
			w,
			"Failed to fetch contact",
			http.StatusInternalServerError,
		)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"contact": contact,
	})
}
