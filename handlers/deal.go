package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"authentication-backend/models"

	"github.com/gorilla/mux"
	"github.com/jackc/pgx/v5"
)

type DealHandler struct {
	DB *pgx.Conn
}

func (h *DealHandler) CreateDeal(w http.ResponseWriter, r *http.Request) {
	var deal models.Deal

	if err := json.NewDecoder(r.Body).Decode(&deal); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if deal.Title == "" {
		http.Error(w, "Deal title is required", http.StatusBadRequest)
		return
	}

	query := `
		INSERT INTO deals (
			title,
			amount,
			stage_id,
			owner_id,
			contact_id,
			monthly_recurring_revenue,
			annual_recurring_revenue
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id
	`

	err := h.DB.QueryRow(
		context.Background(),
		query,
		deal.Title,
		deal.Amount,
		deal.StageID,
		deal.OwnerID,
		deal.ContactID,
		deal.MonthlyRecurringRevenue,
		deal.AnnualRecurringRevenue,
	).Scan(&deal.ID)

	if err != nil {
		http.Error(w, "Failed to create deal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Deal created successfully",
		"deal":    deal,
	})
}

// Get all deals
func (h *DealHandler) GetDeals(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT
			id,
			title,
			COALESCE(amount, 0),
			stage_id,
			owner_id,
			contact_id,
			COALESCE(monthly_recurring_revenue, 0),
			COALESCE(annual_recurring_revenue, 0)
		FROM deals
		ORDER BY id
	`

	rows, err := h.DB.Query(context.Background(), query)
	if err != nil {
		http.Error(w, "Failed to retrieve deals", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	deals := make([]models.Deal, 0)

	for rows.Next() {
		var deal models.Deal

		err := rows.Scan(
			&deal.ID,
			&deal.Title,
			&deal.Amount,
			&deal.StageID,
			&deal.OwnerID,
			&deal.ContactID,
			&deal.MonthlyRecurringRevenue,
			&deal.AnnualRecurringRevenue,
		)
		if err != nil {
			http.Error(w, "Failed to read deal data", http.StatusInternalServerError)
			return
		}

		deals = append(deals, deal)
	}

	if err := rows.Err(); err != nil {
		http.Error(w, "Failed to read deals", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deals": deals,
	})
}

// Get a single deal by ID
func (h *DealHandler) GetDealByID(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid deal ID", http.StatusBadRequest)
		return
	}

	var deal models.Deal

	query := `
		SELECT
			id,
			title,
			COALESCE(amount, 0),
			stage_id,
			owner_id,
			contact_id,
			COALESCE(monthly_recurring_revenue, 0),
			COALESCE(annual_recurring_revenue, 0)
		FROM deals
		WHERE id = $1
	`

	err = h.DB.QueryRow(context.Background(), query, id).Scan(
		&deal.ID,
		&deal.Title,
		&deal.Amount,
		&deal.StageID,
		&deal.OwnerID,
		&deal.ContactID,
		&deal.MonthlyRecurringRevenue,
		&deal.AnnualRecurringRevenue,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			http.Error(w, "Deal not found", http.StatusNotFound)
			return
		}

		http.Error(w, "Failed to retrieve deal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"deal": deal,
	})
}
