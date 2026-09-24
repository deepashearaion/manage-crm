package models

import "time"

type Contact struct {
	ID              int64     `json:"id"`
	FirstName       string    `json:"first_name"`
	LastName        string    `json:"last_name"`
	Email           string    `json:"email"`
	Mobile          string    `json:"mobile"`
	AlternateMobile string    `json:"alternate_mobile"`
	CompanyID       *int64    `json:"company_id"`
	LeadStatusID    *int64    `json:"lead_status_id"`
	LeadOwnerID     *int64    `json:"lead_owner_id"`
	Destination     string    `json:"destination"`
	Source          string    `json:"source"`
	Notes           string    `json:"notes"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
