package handlers

import (
	"encoding/json"
	"net/http"

	"authentication-backend/middleware"
	"authentication-backend/models"

	"github.com/jackc/pgx/v5"
)

type ContactHandler struct {
	DB *pgx.Conn
}

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

	// Request body structure
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

	// Basic validation
	if contact.FirstName == "" {
		http.Error(
			w,
			"First name is required",
			http.StatusBadRequest,
		)
		return
	}

	// Lead owner comes from logged-in user
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
