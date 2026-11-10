// Package invoices issues GST tax invoices: drafts (created by hand or
// when a deal is won), issuing with gapless numbers per financial year and
// a stored PDF, emailing them to the customer, and cancelling. Money is
// integer paise and GST rates are basis points (1800 = 18%).
package invoices

import (
	"context"
	"errors"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/principal"
)

const (
	StatusDraft     = "draft"
	StatusIssued    = "issued"
	StatusCancelled = "cancelled"
)

// Profile is the org's billing profile: its details as a seller and its
// invoice defaults.
type Profile struct {
	LegalName        string    `json:"legal_name"`
	GSTIN            string    `json:"gstin"`
	StateCode        string    `json:"state_code"`
	Address          string    `json:"address"`
	Email            string    `json:"email"`
	Phone            string    `json:"phone"`
	InvoicePrefix    string    `json:"invoice_prefix"`
	DefaultSAC       string    `json:"default_sac"`
	DefaultGSTRateBP int       `json:"default_gst_rate_bp"`
	PricesIncludeGST bool      `json:"prices_include_gst"`
	PaymentTermsDays int       `json:"payment_terms_days"`
	BankDetails      string    `json:"bank_details"`
	Terms            string    `json:"terms"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// DefaultProfile applies until an org saves its own.
var DefaultProfile = Profile{InvoicePrefix: "INV", DefaultGSTRateBP: 1800, PaymentTermsDays: 7}

// ProfileInput changes the billing profile; nil fields are unchanged.
type ProfileInput struct {
	LegalName        *string `json:"legal_name"`
	GSTIN            *string `json:"gstin"`
	StateCode        *string `json:"state_code"`
	Address          *string `json:"address"`
	Email            *string `json:"email"`
	Phone            *string `json:"phone"`
	InvoicePrefix    *string `json:"invoice_prefix"`
	DefaultSAC       *string `json:"default_sac"`
	DefaultGSTRateBP *int    `json:"default_gst_rate_bp"`
	PricesIncludeGST *bool   `json:"prices_include_gst"`
	PaymentTermsDays *int    `json:"payment_terms_days"`
	BankDetails      *string `json:"bank_details"`
	Terms            *string `json:"terms"`
}

type Line struct {
	Description    string `json:"description"`
	SAC            string `json:"sac"`
	Quantity       int    `json:"quantity"`
	UnitPricePaise int64  `json:"unit_price_paise"`
	GSTRateBP      int    `json:"gst_rate_bp"`
	TaxablePaise   int64  `json:"taxable_paise"`
	CGSTPaise      int64  `json:"cgst_paise"`
	SGSTPaise      int64  `json:"sgst_paise"`
	IGSTPaise      int64  `json:"igst_paise"`
	TotalPaise     int64  `json:"total_paise"`
}

// LineInput is a line to put on a draft; nil SAC and rate take the
// profile's defaults.
type LineInput struct {
	Description    string  `json:"description"`
	SAC            *string `json:"sac"`
	Quantity       int     `json:"quantity"`
	UnitPricePaise int64   `json:"unit_price_paise"`
	GSTRateBP      *int    `json:"gst_rate_bp"`
}

type Invoice struct {
	ID               int64      `json:"id"`
	Status           string     `json:"status"`
	Number           *string    `json:"number"`
	FinancialYear    *string    `json:"financial_year"`
	DealID           *int64     `json:"deal_id"`
	ContactID        *int64     `json:"contact_id"`
	OwnerID          *int64     `json:"owner_id"`
	BillToName       string     `json:"bill_to_name"`
	BillToAddress    string     `json:"bill_to_address"`
	BillToGSTIN      string     `json:"bill_to_gstin"`
	BillToEmail      string     `json:"bill_to_email"`
	BillToPhone      string     `json:"bill_to_phone"`
	PlaceOfSupply    string     `json:"place_of_supply"`
	SellerName       string     `json:"seller_name"`
	SellerGSTIN      string     `json:"seller_gstin"`
	SellerStateCode  string     `json:"seller_state_code"`
	SellerAddress    string     `json:"seller_address"`
	PricesIncludeGST bool       `json:"prices_include_gst"`
	IssueDate        *string    `json:"issue_date"`
	DueDate          *string    `json:"due_date"`
	Notes            string     `json:"notes"`
	TaxablePaise     int64      `json:"taxable_paise"`
	CGSTPaise        int64      `json:"cgst_paise"`
	SGSTPaise        int64      `json:"sgst_paise"`
	IGSTPaise        int64      `json:"igst_paise"`
	RoundOffPaise    int64      `json:"round_off_paise"`
	TotalPaise       int64      `json:"total_paise"`
	Lines            []Line     `json:"lines"`
	IssuedAt         *time.Time `json:"issued_at"`
	SentAt           *time.Time `json:"sent_at"`
	CancelledAt      *time.Time `json:"cancelled_at"`
	CancelReason     string     `json:"cancel_reason"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`

	sellerBankDetails string
	sellerTerms       string
}

// Input creates a draft. Lines may be empty when DealID is set: the deal's
// amount becomes the only line.
type Input struct {
	DealID           *int64      `json:"deal_id"`
	ContactID        *int64      `json:"contact_id"`
	BillToName       *string     `json:"bill_to_name"`
	BillToAddress    *string     `json:"bill_to_address"`
	BillToGSTIN      *string     `json:"bill_to_gstin"`
	BillToEmail      *string     `json:"bill_to_email"`
	BillToPhone      *string     `json:"bill_to_phone"`
	PlaceOfSupply    *string     `json:"place_of_supply"`
	PricesIncludeGST *bool       `json:"prices_include_gst"`
	DueDate          *string     `json:"due_date"`
	Notes            *string     `json:"notes"`
	Lines            []LineInput `json:"lines"`
}

// Patch changes a draft; nil fields are unchanged and Lines, when set,
// replaces every line.
type Patch struct {
	BillToName       *string      `json:"bill_to_name"`
	BillToAddress    *string      `json:"bill_to_address"`
	BillToGSTIN      *string      `json:"bill_to_gstin"`
	BillToEmail      *string      `json:"bill_to_email"`
	BillToPhone      *string      `json:"bill_to_phone"`
	PlaceOfSupply    *string      `json:"place_of_supply"`
	PricesIncludeGST *bool        `json:"prices_include_gst"`
	DueDate          *string      `json:"due_date"`
	Notes            *string      `json:"notes"`
	Lines            *[]LineInput `json:"lines"`
}

// Filter narrows an invoice list.
type Filter struct {
	Status    string
	ContactID int64
	DealID    int64
	Limit     int
	Offset    int
}

// Deal is what an invoice needs from a deal.
type Deal struct {
	ID            int64
	Title         string
	ContactID     *int64
	OwnerID       *int64
	AmountPaise   int64
	BillingType   string
	BillingPeriod *string
}

// Customer is what an invoice needs from a contact.
type Customer struct {
	Name  string
	Email string
	Phone string
}

// Contacts is what this module needs from the contacts module.
type Contacts interface {
	Visible(ctx context.Context, p principal.Principal, contactID int64) error
	Customer(ctx context.Context, orgID, contactID int64) (Customer, error)
	Record(ctx context.Context, orgID, contactID int64, kind, summary string, meta map[string]any, actor *int64) error
}

// Deals is what this module needs from the deals module.
type Deals interface {
	// Visible returns the deal if p may see it, else ErrDealNotFound.
	Visible(ctx context.Context, p principal.Principal, dealID int64) (Deal, error)
}

// FieldError is a validation failure on one request field.
type FieldError struct {
	Field, Problem, Message string
}

func (e *FieldError) Error() string { return e.Message }

func fieldErr(field, problem, msg string) error { return &FieldError{field, problem, msg} }

// IncompleteError lists what must be filled in before issuing.
type IncompleteError struct {
	Fields map[string]string
}

func (e *IncompleteError) Error() string { return "invoice cannot be issued yet" }

var (
	ErrNotFound        = errors.New("invoice not found")
	ErrForbidden       = errors.New("not allowed")
	ErrContactNotFound = errors.New("contact not found")
	ErrDealNotFound    = errors.New("deal not found")
	ErrNotDraft        = errors.New("only drafts can be changed")
	ErrNotIssued       = errors.New("only issued invoices can be sent or cancelled")
	ErrDealInvoiced    = errors.New("this deal already has an invoice")
	ErrNoEmail         = errors.New("the invoice has no customer email")
)
