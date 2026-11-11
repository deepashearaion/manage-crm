package invoices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/deepashearaion/manage-crm/internal/platform/mail"
	"github.com/deepashearaion/manage-crm/internal/platform/principal"
	"github.com/deepashearaion/manage-crm/internal/platform/validate"
)

type Service struct {
	repo     *Repository
	contacts Contacts
	deals    Deals
	mailer   mail.Mailer
	logger   *slog.Logger
	now      func() time.Time
}

func NewService(repo *Repository, contacts Contacts, deals Deals, mailer mail.Mailer, logger *slog.Logger, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, contacts: contacts, deals: deals, mailer: mailer, logger: logger, now: now}
}

const module = principal.Invoices

// scope is what p may see: their org, and only the invoices they own unless
// their role sees all invoices.
func scope(p principal.Principal) Scope {
	s := Scope{OrgID: p.OrgID}
	if !p.SeesAll(module) {
		s.OwnerID = &p.UserID
	}
	return s
}

// Billing profile

func (s *Service) Profile(ctx context.Context, p principal.Principal) (Profile, error) {
	return s.repo.Profile(ctx, p.OrgID)
}

var prefixPattern = regexp.MustCompile(`^[A-Z0-9]{1,5}$`)

func (s *Service) SaveProfile(ctx context.Context, p principal.Principal, in ProfileInput) (Profile, error) {
	if !p.Can(principal.Settings, principal.Edit) {
		return Profile{}, ErrForbidden
	}
	pr, err := s.repo.Profile(ctx, p.OrgID)
	if err != nil {
		return Profile{}, err
	}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&pr.LegalName, in.LegalName)
	set(&pr.GSTIN, in.GSTIN)
	set(&pr.StateCode, in.StateCode)
	set(&pr.Address, in.Address)
	set(&pr.Email, in.Email)
	set(&pr.Phone, in.Phone)
	set(&pr.InvoicePrefix, in.InvoicePrefix)
	set(&pr.DefaultSAC, in.DefaultSAC)
	set(&pr.BankDetails, in.BankDetails)
	set(&pr.Terms, in.Terms)
	if in.DefaultGSTRateBP != nil {
		pr.DefaultGSTRateBP = *in.DefaultGSTRateBP
	}
	if in.PricesIncludeGST != nil {
		pr.PricesIncludeGST = *in.PricesIncludeGST
	}
	if in.PaymentTermsDays != nil {
		pr.PaymentTermsDays = *in.PaymentTermsDays
	}
	pr.GSTIN = strings.ToUpper(pr.GSTIN)
	pr.InvoicePrefix = strings.ToUpper(pr.InvoicePrefix)
	pr.Email = validate.NormalizeEmail(pr.Email)
	switch {
	case utf8.RuneCountInString(pr.LegalName) > 200:
		return Profile{}, fieldErr("legal_name", "invalid", "Legal name must be at most 200 characters.")
	case pr.GSTIN != "" && !ValidGSTIN(pr.GSTIN):
		return Profile{}, fieldErr("gstin", "invalid", "This is not a valid GSTIN.")
	case pr.StateCode != "" && States[pr.StateCode] == "":
		return Profile{}, fieldErr("state_code", "invalid", "Use a two-digit GST state code, e.g. 27 for Maharashtra.")
	case pr.GSTIN != "" && pr.StateCode != "" && pr.GSTIN[:2] != pr.StateCode:
		return Profile{}, fieldErr("state_code", "invalid", "The state code must match the GSTIN's first two digits.")
	case pr.Email != "" && !validate.Email(pr.Email):
		return Profile{}, fieldErr("email", "invalid", "Invalid email address.")
	case !prefixPattern.MatchString(pr.InvoicePrefix):
		return Profile{}, fieldErr("invoice_prefix", "invalid", "The invoice prefix is 1-5 capital letters or digits.")
	case pr.DefaultSAC != "" && !sacPattern.MatchString(pr.DefaultSAC):
		return Profile{}, fieldErr("default_sac", "invalid", "SAC/HSN codes are 4-8 digits.")
	case pr.DefaultGSTRateBP < 0 || pr.DefaultGSTRateBP > 10000:
		return Profile{}, fieldErr("default_gst_rate_bp", "invalid", "The GST rate is in basis points, 0-10000 (1800 = 18%).")
	case pr.PaymentTermsDays < 0 || pr.PaymentTermsDays > 365:
		return Profile{}, fieldErr("payment_terms_days", "invalid", "Payment terms must be 0-365 days.")
	}
	if pr.GSTIN != "" && pr.StateCode == "" {
		pr.StateCode = pr.GSTIN[:2]
	}
	return s.repo.SaveProfile(ctx, p.OrgID, pr)
}

// Drafts

const (
	maxQuantity  = 1_000_000
	maxUnitPrice = 100_000_000_000_00 // ₹1,000 crore in paise
)

// buildLines validates line inputs and applies the profile's defaults.
func buildLines(in []LineInput, pr Profile) ([]Line, error) {
	if len(in) > 100 {
		return nil, fieldErr("lines", "invalid", "An invoice can have at most 100 lines.")
	}
	lines := make([]Line, 0, len(in))
	for i, li := range in {
		field := func(f string) string { return fmt.Sprintf("lines[%d].%s", i, f) }
		l := Line{Description: strings.TrimSpace(li.Description), SAC: pr.DefaultSAC, Quantity: li.Quantity,
			UnitPricePaise: li.UnitPricePaise, GSTRateBP: pr.DefaultGSTRateBP}
		if li.SAC != nil {
			l.SAC = strings.TrimSpace(*li.SAC)
		}
		if li.GSTRateBP != nil {
			l.GSTRateBP = *li.GSTRateBP
		}
		switch {
		case l.Description == "" || utf8.RuneCountInString(l.Description) > 1000:
			return nil, fieldErr(field("description"), "required", "Each line needs a description (up to 1000 characters).")
		case l.SAC != "" && !sacPattern.MatchString(l.SAC):
			return nil, fieldErr(field("sac"), "invalid", "SAC/HSN codes are 4-8 digits.")
		case l.Quantity < 1 || l.Quantity > maxQuantity:
			return nil, fieldErr(field("quantity"), "invalid", "Quantity must be a whole number from 1 to 1,000,000.")
		case l.UnitPricePaise < 0 || l.UnitPricePaise > maxUnitPrice:
			return nil, fieldErr(field("unit_price_paise"), "invalid", "Unit price must be 0 to ₹1,000 crore, in paise.")
		case l.GSTRateBP < 0 || l.GSTRateBP > 10000:
			return nil, fieldErr(field("gst_rate_bp"), "invalid", "The GST rate is in basis points, 0-10000 (1800 = 18%).")
		}
		lines = append(lines, l)
	}
	return lines, nil
}

// checkParty validates the customer details and fills the place of supply
// from the customer's GSTIN when it is not set.
func checkParty(inv *Invoice) error {
	inv.BillToGSTIN = strings.ToUpper(strings.TrimSpace(inv.BillToGSTIN))
	inv.BillToEmail = validate.NormalizeEmail(inv.BillToEmail)
	switch {
	case utf8.RuneCountInString(inv.BillToName) > 200:
		return fieldErr("bill_to_name", "invalid", "Name must be at most 200 characters.")
	case inv.BillToGSTIN != "" && !ValidGSTIN(inv.BillToGSTIN):
		return fieldErr("bill_to_gstin", "invalid", "This is not a valid GSTIN.")
	case inv.BillToEmail != "" && !validate.Email(inv.BillToEmail):
		return fieldErr("bill_to_email", "invalid", "Invalid email address.")
	case inv.PlaceOfSupply != "" && States[inv.PlaceOfSupply] == "":
		return fieldErr("place_of_supply", "invalid", "Use a two-digit GST state code, e.g. 29 for Karnataka.")
	}
	if inv.PlaceOfSupply == "" && inv.BillToGSTIN != "" {
		inv.PlaceOfSupply = inv.BillToGSTIN[:2]
	}
	if inv.DueDate != nil {
		if _, err := time.Parse("2006-01-02", *inv.DueDate); err != nil {
			return fieldErr("due_date", "invalid", "Due date must be YYYY-MM-DD.")
		}
	}
	return nil
}

// dealLine is the single line a deal becomes on its invoice.
func dealLine(d Deal) LineInput {
	desc := d.Title
	if d.BillingType == "recurring" && d.BillingPeriod != nil {
		desc += " (" + *d.BillingPeriod + " subscription)"
	}
	return LineInput{Description: desc, Quantity: 1, UnitPricePaise: d.AmountPaise}
}

// Create adds a draft. With a deal, the deal's contact, owner and amount
// fill in what the input leaves out.
func (s *Service) Create(ctx context.Context, p principal.Principal, in Input) (Invoice, error) {
	if !p.Can(module, principal.Create) {
		return Invoice{}, ErrForbidden
	}
	pr, err := s.repo.Profile(ctx, p.OrgID)
	if err != nil {
		return Invoice{}, err
	}
	inv := Invoice{ContactID: in.ContactID, OwnerID: &p.UserID, PricesIncludeGST: pr.PricesIncludeGST,
		SellerStateCode: pr.StateCode, DueDate: in.DueDate}
	lineInputs := in.Lines
	if in.DealID != nil {
		d, err := s.deals.Visible(ctx, p, *in.DealID)
		if err != nil {
			return Invoice{}, err
		}
		inv.DealID = &d.ID
		if inv.ContactID == nil {
			inv.ContactID = d.ContactID
		}
		if d.OwnerID != nil {
			inv.OwnerID = d.OwnerID
		}
		if len(lineInputs) == 0 {
			lineInputs = []LineInput{dealLine(d)}
		}
	}
	// An invoice for someone else's deal belongs to them, which needs create
	// access beyond one's own invoices.
	if !p.CanOn(module, principal.Create, inv.OwnerID) {
		return Invoice{}, ErrForbidden
	}
	if inv.ContactID != nil {
		if err := s.contacts.Visible(ctx, p, *inv.ContactID); err != nil {
			return Invoice{}, err
		}
		c, err := s.contacts.Customer(ctx, p.OrgID, *inv.ContactID)
		if err != nil {
			return Invoice{}, err
		}
		inv.BillToName, inv.BillToEmail, inv.BillToPhone = c.Name, c.Email, c.Phone
	}
	applyParty(&inv, in.BillToName, in.BillToAddress, in.BillToGSTIN, in.BillToEmail, in.BillToPhone, in.PlaceOfSupply, in.Notes)
	if in.PricesIncludeGST != nil {
		inv.PricesIncludeGST = *in.PricesIncludeGST
	}
	if err := checkParty(&inv); err != nil {
		return Invoice{}, err
	}
	if inv.Lines, err = buildLines(lineInputs, pr); err != nil {
		return Invoice{}, err
	}
	computeTotals(&inv)
	id, err := s.repo.CreateDraft(ctx, p.OrgID, inv, &p.UserID)
	if err != nil {
		return Invoice{}, err
	}
	return s.repo.Get(ctx, Scope{OrgID: p.OrgID}, id)
}

func applyParty(inv *Invoice, name, address, gstin, email, phone, pos, notes *string) {
	for _, f := range []struct {
		dst *string
		v   *string
	}{{&inv.BillToName, name}, {&inv.BillToAddress, address}, {&inv.BillToGSTIN, gstin}, {&inv.BillToEmail, email},
		{&inv.BillToPhone, phone}, {&inv.PlaceOfSupply, pos}, {&inv.Notes, notes}} {
		if f.v != nil {
			*f.dst = strings.TrimSpace(*f.v)
		}
	}
}

// CreateForWonDeal drafts the invoice for a deal that was just won, unless
// it already has one. It reports whether it created one.
func (s *Service) CreateForWonDeal(ctx context.Context, orgID int64, d Deal) (Invoice, bool, error) {
	pr, err := s.repo.Profile(ctx, orgID)
	if err != nil {
		return Invoice{}, false, err
	}
	inv := Invoice{DealID: &d.ID, ContactID: d.ContactID, OwnerID: d.OwnerID,
		PricesIncludeGST: pr.PricesIncludeGST, SellerStateCode: pr.StateCode}
	if d.ContactID != nil {
		c, err := s.contacts.Customer(ctx, orgID, *d.ContactID)
		if err != nil && !errors.Is(err, ErrContactNotFound) {
			return Invoice{}, false, err
		}
		inv.BillToName, inv.BillToEmail, inv.BillToPhone = c.Name, c.Email, c.Phone
		if checkParty(&inv) != nil { // e.g. a malformed email on the contact
			inv.BillToEmail = ""
		}
	}
	if inv.Lines, err = buildLines([]LineInput{dealLine(d)}, pr); err != nil {
		return Invoice{}, false, err
	}
	computeTotals(&inv)
	id, err := s.repo.CreateDraft(ctx, orgID, inv, nil)
	if errors.Is(err, ErrDealInvoiced) {
		return Invoice{}, false, nil
	}
	if err != nil {
		return Invoice{}, false, err
	}
	inv, err = s.repo.Get(ctx, Scope{OrgID: orgID}, id)
	return inv, err == nil, err
}

func (s *Service) List(ctx context.Context, p principal.Principal, f Filter) ([]Invoice, error) {
	if !p.Can(module, principal.View) {
		return nil, ErrForbidden
	}
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	return s.repo.List(ctx, scope(p), f)
}

func (s *Service) Get(ctx context.Context, p principal.Principal, id int64) (Invoice, error) {
	if !p.Can(module, principal.View) {
		return Invoice{}, ErrForbidden
	}
	return s.repo.Get(ctx, scope(p), id)
}

// writable loads an invoice p may perform action (edit or delete) on.
func (s *Service) writable(ctx context.Context, p principal.Principal, id int64, action principal.Action) (Invoice, error) {
	inv, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Invoice{}, err
	}
	if !p.CanOn(module, action, inv.OwnerID) {
		return Invoice{}, ErrForbidden
	}
	return inv, nil
}

func (s *Service) Update(ctx context.Context, p principal.Principal, id int64, patch Patch) (Invoice, error) {
	inv, err := s.writable(ctx, p, id, principal.Edit)
	if err != nil {
		return Invoice{}, err
	}
	if inv.Status != StatusDraft {
		return Invoice{}, ErrNotDraft
	}
	pr, err := s.repo.Profile(ctx, p.OrgID)
	if err != nil {
		return Invoice{}, err
	}
	applyParty(&inv, patch.BillToName, patch.BillToAddress, patch.BillToGSTIN, patch.BillToEmail, patch.BillToPhone,
		patch.PlaceOfSupply, patch.Notes)
	if patch.PricesIncludeGST != nil {
		inv.PricesIncludeGST = *patch.PricesIncludeGST
	}
	if patch.DueDate != nil {
		inv.DueDate = patch.DueDate
	}
	if err := checkParty(&inv); err != nil {
		return Invoice{}, err
	}
	if patch.Lines != nil {
		if inv.Lines, err = buildLines(*patch.Lines, pr); err != nil {
			return Invoice{}, err
		}
	}
	inv.SellerStateCode = pr.StateCode
	computeTotals(&inv)
	if err := s.repo.SaveDraft(ctx, scope(p), inv); err != nil {
		return Invoice{}, err
	}
	return s.repo.Get(ctx, scope(p), id)
}

func (s *Service) Delete(ctx context.Context, p principal.Principal, id int64) error {
	if _, err := s.writable(ctx, p, id, principal.Delete); err != nil {
		return err
	}
	return s.repo.DeleteDraft(ctx, scope(p), id)
}

// Issue assigns the next number of the current financial year, snapshots
// the seller's details, recomputes the tax, renders and stores the PDF.
// Everything a GST tax invoice requires must be filled in first.
func (s *Service) Issue(ctx context.Context, p principal.Principal, id int64) (Invoice, error) {
	inv, err := s.writable(ctx, p, id, principal.Edit)
	if err != nil {
		return Invoice{}, err
	}
	if inv.Status != StatusDraft {
		return Invoice{}, ErrNotDraft
	}
	pr, err := s.repo.Profile(ctx, p.OrgID)
	if err != nil {
		return Invoice{}, err
	}
	missing := map[string]string{}
	need := func(ok bool, field string) {
		if !ok {
			missing[field] = "required"
		}
	}
	need(pr.LegalName != "", "billing_profile.legal_name")
	need(pr.GSTIN != "", "billing_profile.gstin")
	need(pr.StateCode != "", "billing_profile.state_code")
	need(pr.Address != "", "billing_profile.address")
	need(inv.BillToName != "", "bill_to_name")
	need(inv.PlaceOfSupply != "", "place_of_supply")
	need(len(inv.Lines) > 0, "lines")
	for i, l := range inv.Lines {
		need(l.SAC != "", fmt.Sprintf("lines[%d].sac", i))
	}
	if len(missing) > 0 {
		return Invoice{}, &IncompleteError{Fields: missing}
	}

	today := s.now().In(ist)
	issued, err := s.repo.Issue(ctx, scope(p), id, today, pr.InvoicePrefix, p.UserID, func(inv *Invoice) ([]byte, error) {
		inv.SellerName, inv.SellerGSTIN, inv.SellerStateCode = pr.LegalName, pr.GSTIN, pr.StateCode
		inv.SellerAddress, inv.sellerBankDetails, inv.sellerTerms = pr.Address, pr.BankDetails, pr.Terms
		if inv.DueDate == nil {
			due := today.AddDate(0, 0, pr.PaymentTermsDays).Format("2006-01-02")
			inv.DueDate = &due
		}
		computeTotals(inv)
		return renderPDF(*inv)
	})
	if err != nil {
		return Invoice{}, err
	}
	s.timeline(ctx, p.OrgID, issued, "invoice_issued",
		fmt.Sprintf("Invoice %s issued: %s", *issued.Number, formatRupees(issued.TotalPaise)), &p.UserID)
	return issued, nil
}

// PDF returns the stored PDF of an issued (or cancelled) invoice, or a
// preview marked DRAFT for a draft. It also returns a file name.
func (s *Service) PDF(ctx context.Context, p principal.Principal, id int64) ([]byte, string, error) {
	if !p.Can(module, principal.View) {
		return nil, "", ErrForbidden
	}
	inv, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return nil, "", err
	}
	if inv.Status == StatusDraft {
		pr, err := s.repo.Profile(ctx, p.OrgID)
		if err != nil {
			return nil, "", err
		}
		inv.SellerName, inv.SellerGSTIN, inv.SellerAddress = pr.LegalName, pr.GSTIN, pr.Address
		inv.sellerBankDetails, inv.sellerTerms = pr.BankDetails, pr.Terms
		pdf, err := renderPDF(inv)
		return pdf, fmt.Sprintf("draft-invoice-%d.pdf", inv.ID), err
	}
	pdf, err := s.repo.PDF(ctx, scope(p), id)
	return pdf, fileName(inv), err
}

func fileName(inv Invoice) string {
	return strings.ReplaceAll(*inv.Number, "/", "-") + ".pdf"
}

// Send emails an issued invoice's PDF to the customer.
func (s *Service) Send(ctx context.Context, p principal.Principal, id int64) (Invoice, error) {
	inv, err := s.writable(ctx, p, id, principal.Edit)
	if err != nil {
		return Invoice{}, err
	}
	if inv.Status != StatusIssued {
		return Invoice{}, ErrNotIssued
	}
	if inv.BillToEmail == "" {
		return Invoice{}, ErrNoEmail
	}
	pdf, err := s.repo.PDF(ctx, scope(p), id)
	if err != nil {
		return Invoice{}, err
	}
	text := fmt.Sprintf("Dear %s,\n\nPlease find attached invoice %s for %s, due on %s.\n\nThank you,\n%s",
		inv.BillToName, *inv.Number, formatRupees(inv.TotalPaise), deref(inv.DueDate), inv.SellerName)
	if err := s.mailer.Send(ctx, mail.Message{
		To: inv.BillToEmail, Subject: fmt.Sprintf("Invoice %s from %s", *inv.Number, inv.SellerName), Text: text,
		Attachments: []mail.Attachment{{Name: fileName(inv), ContentType: "application/pdf", Data: pdf}},
	}); err != nil {
		return Invoice{}, err
	}
	if err := s.repo.MarkSent(ctx, p.OrgID, id); err != nil {
		return Invoice{}, err
	}
	s.timeline(ctx, p.OrgID, inv, "invoice_sent", fmt.Sprintf("Invoice %s emailed to %s", *inv.Number, inv.BillToEmail), &p.UserID)
	return s.repo.Get(ctx, scope(p), id)
}

// Cancel cancels an issued invoice (needs invoices.delete for all invoices). Its number is not
// reused; issue a new invoice to replace it.
func (s *Service) Cancel(ctx context.Context, p principal.Principal, id int64, reason string) (Invoice, error) {
	if p.Scope(module, principal.Delete) != principal.All {
		return Invoice{}, ErrForbidden
	}
	inv, err := s.repo.Get(ctx, scope(p), id)
	if err != nil {
		return Invoice{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 500 {
		return Invoice{}, fieldErr("reason", "required", "Give a reason (up to 500 characters).")
	}
	if err := s.repo.Cancel(ctx, scope(p), id, reason); err != nil {
		return Invoice{}, err
	}
	s.timeline(ctx, p.OrgID, inv, "invoice_cancelled", fmt.Sprintf("Invoice %s cancelled: %s", deref(inv.Number), reason), &p.UserID)
	return s.repo.Get(ctx, scope(p), id)
}

func (s *Service) timeline(ctx context.Context, orgID int64, inv Invoice, kind, summary string, actor *int64) {
	if inv.ContactID == nil {
		return
	}
	err := s.contacts.Record(ctx, orgID, *inv.ContactID, kind, summary,
		map[string]any{"invoice_id": inv.ID, "number": deref(inv.Number), "total_paise": inv.TotalPaise}, actor)
	if err != nil && !errors.Is(err, ErrContactNotFound) {
		s.logger.ErrorContext(ctx, "invoice timeline entry", slog.Any("error", err))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
