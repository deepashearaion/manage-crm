package invoices

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the only code that touches billing_profiles, invoices,
// invoice_lines and invoice_counters.
type Repository struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}

func mapErr(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.ConstraintName {
		case "invoices_deal_live_key":
			return ErrDealInvoiced
		case "invoices_contact_same_org_fkey":
			return ErrContactNotFound
		case "invoices_deal_same_org_fkey":
			return ErrDealNotFound
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Profile

func (r *Repository) Profile(ctx context.Context, orgID int64) (Profile, error) {
	var p Profile
	err := r.db.QueryRow(ctx,
		`SELECT legal_name, gstin, state_code, address, email, phone, invoice_prefix, default_sac,
			default_gst_rate_bp, prices_include_gst, payment_terms_days, bank_details, terms, updated_at
		FROM billing_profiles WHERE org_id = $1`, orgID,
	).Scan(&p.LegalName, &p.GSTIN, &p.StateCode, &p.Address, &p.Email, &p.Phone, &p.InvoicePrefix, &p.DefaultSAC,
		&p.DefaultGSTRateBP, &p.PricesIncludeGST, &p.PaymentTermsDays, &p.BankDetails, &p.Terms, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultProfile, nil
	}
	return p, err
}

func (r *Repository) SaveProfile(ctx context.Context, orgID int64, p Profile) (Profile, error) {
	_, err := r.db.Exec(ctx,
		`INSERT INTO billing_profiles (org_id, legal_name, gstin, state_code, address, email, phone, invoice_prefix,
			default_sac, default_gst_rate_bp, prices_include_gst, payment_terms_days, bank_details, terms, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, now())
		ON CONFLICT (org_id) DO UPDATE SET
			legal_name = EXCLUDED.legal_name, gstin = EXCLUDED.gstin, state_code = EXCLUDED.state_code,
			address = EXCLUDED.address, email = EXCLUDED.email, phone = EXCLUDED.phone,
			invoice_prefix = EXCLUDED.invoice_prefix, default_sac = EXCLUDED.default_sac,
			default_gst_rate_bp = EXCLUDED.default_gst_rate_bp, prices_include_gst = EXCLUDED.prices_include_gst,
			payment_terms_days = EXCLUDED.payment_terms_days, bank_details = EXCLUDED.bank_details,
			terms = EXCLUDED.terms, updated_at = EXCLUDED.updated_at`,
		orgID, p.LegalName, p.GSTIN, p.StateCode, p.Address, p.Email, p.Phone, p.InvoicePrefix,
		p.DefaultSAC, p.DefaultGSTRateBP, p.PricesIncludeGST, p.PaymentTermsDays, p.BankDetails, p.Terms)
	if err != nil {
		return Profile{}, err
	}
	return r.Profile(ctx, orgID)
}

// Invoices

// Scope limits queries to an org and, when OwnerID is set, to that user's invoices.
type Scope struct {
	OrgID   int64
	OwnerID *int64
}

const columns = `id, status, number, financial_year, deal_id, contact_id, owner_id,
	bill_to_name, bill_to_address, bill_to_gstin, bill_to_email, bill_to_phone, place_of_supply,
	seller_name, seller_gstin, seller_state_code, seller_address, seller_bank_details, seller_terms,
	prices_include_gst, to_char(issue_date, 'YYYY-MM-DD'), to_char(due_date, 'YYYY-MM-DD'), notes,
	taxable_paise, cgst_paise, sgst_paise, igst_paise, round_off_paise, total_paise,
	issued_at, sent_at, cancelled_at, cancel_reason, created_at, updated_at`

const scopeFilter = `org_id = $1 AND ($2::bigint IS NULL OR owner_id = $2)`

func scan(row pgx.Row) (Invoice, error) {
	var i Invoice
	err := row.Scan(&i.ID, &i.Status, &i.Number, &i.FinancialYear, &i.DealID, &i.ContactID, &i.OwnerID,
		&i.BillToName, &i.BillToAddress, &i.BillToGSTIN, &i.BillToEmail, &i.BillToPhone, &i.PlaceOfSupply,
		&i.SellerName, &i.SellerGSTIN, &i.SellerStateCode, &i.SellerAddress, &i.sellerBankDetails, &i.sellerTerms,
		&i.PricesIncludeGST, &i.IssueDate, &i.DueDate, &i.Notes,
		&i.TaxablePaise, &i.CGSTPaise, &i.SGSTPaise, &i.IGSTPaise, &i.RoundOffPaise, &i.TotalPaise,
		&i.IssuedAt, &i.SentAt, &i.CancelledAt, &i.CancelReason, &i.CreatedAt, &i.UpdatedAt)
	return i, mapErr(err)
}

func (r *Repository) lines(ctx context.Context, q pgx.Tx, id int64) ([]Line, error) {
	var rows pgx.Rows
	var err error
	sql := `SELECT description, sac, quantity, unit_price_paise, gst_rate_bp, taxable_paise, cgst_paise, sgst_paise,
		igst_paise, total_paise FROM invoice_lines WHERE invoice_id = $1 ORDER BY position`
	if q != nil {
		rows, err = q.Query(ctx, sql, id)
	} else {
		rows, err = r.db.Query(ctx, sql, id)
	}
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Line, error) {
		var l Line
		err := row.Scan(&l.Description, &l.SAC, &l.Quantity, &l.UnitPricePaise, &l.GSTRateBP, &l.TaxablePaise,
			&l.CGSTPaise, &l.SGSTPaise, &l.IGSTPaise, &l.TotalPaise)
		return l, err
	})
}

func (r *Repository) Get(ctx context.Context, s Scope, id int64) (Invoice, error) {
	inv, err := scan(r.db.QueryRow(ctx, `SELECT `+columns+` FROM invoices WHERE `+scopeFilter+` AND id = $3`,
		s.OrgID, s.OwnerID, id))
	if err != nil {
		return Invoice{}, err
	}
	inv.Lines, err = r.lines(ctx, nil, id)
	return inv, err
}

// List returns invoices without their lines, newest first.
func (r *Repository) List(ctx context.Context, s Scope, f Filter) ([]Invoice, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+columns+` FROM invoices WHERE `+scopeFilter+`
			AND ($3 = '' OR status = $3) AND ($4::bigint = 0 OR contact_id = $4) AND ($5::bigint = 0 OR deal_id = $5)
		ORDER BY id DESC LIMIT $6 OFFSET $7`,
		s.OrgID, s.OwnerID, f.Status, f.ContactID, f.DealID, f.Limit, f.Offset)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invoice, error) { return scan(row) })
}

func writeLines(ctx context.Context, tx pgx.Tx, orgID, id int64, lines []Line) error {
	if _, err := tx.Exec(ctx, `DELETE FROM invoice_lines WHERE invoice_id = $1`, id); err != nil {
		return err
	}
	for i, l := range lines {
		if _, err := tx.Exec(ctx,
			`INSERT INTO invoice_lines (org_id, invoice_id, position, description, sac, quantity, unit_price_paise,
				gst_rate_bp, taxable_paise, cgst_paise, sgst_paise, igst_paise, total_paise)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			orgID, id, i, l.Description, l.SAC, l.Quantity, l.UnitPricePaise, l.GSTRateBP,
			l.TaxablePaise, l.CGSTPaise, l.SGSTPaise, l.IGSTPaise, l.TotalPaise,
		); err != nil {
			return err
		}
	}
	return nil
}

// CreateDraft inserts a draft with its lines.
func (r *Repository) CreateDraft(ctx context.Context, orgID int64, inv Invoice, by *int64) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO invoices (org_id, deal_id, contact_id, owner_id, bill_to_name, bill_to_address, bill_to_gstin,
				bill_to_email, bill_to_phone, place_of_supply, seller_state_code, prices_include_gst, due_date, notes,
				taxable_paise, cgst_paise, sgst_paise, igst_paise, round_off_paise, total_paise, created_by_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13::date, $14, $15, $16, $17, $18, $19, $20, $21)
			RETURNING id`,
			orgID, inv.DealID, inv.ContactID, inv.OwnerID, inv.BillToName, inv.BillToAddress, inv.BillToGSTIN,
			inv.BillToEmail, inv.BillToPhone, inv.PlaceOfSupply, inv.SellerStateCode, inv.PricesIncludeGST, inv.DueDate,
			inv.Notes, inv.TaxablePaise, inv.CGSTPaise, inv.SGSTPaise, inv.IGSTPaise, inv.RoundOffPaise, inv.TotalPaise, by,
		).Scan(&id); err != nil {
			return mapErr(err)
		}
		return writeLines(ctx, tx, orgID, id, inv.Lines)
	})
	return id, err
}

// SaveDraft writes a draft's editable fields and lines; ErrNotDraft if it
// was issued meanwhile.
func (r *Repository) SaveDraft(ctx context.Context, s Scope, inv Invoice) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE invoices SET bill_to_name = $4, bill_to_address = $5, bill_to_gstin = $6, bill_to_email = $7,
				bill_to_phone = $8, place_of_supply = $9, seller_state_code = $10, prices_include_gst = $11,
				due_date = $12::date, notes = $13, taxable_paise = $14, cgst_paise = $15, sgst_paise = $16,
				igst_paise = $17, round_off_paise = $18, total_paise = $19, updated_at = now()
			WHERE `+scopeFilter+` AND id = $3 AND status = 'draft'`,
			s.OrgID, s.OwnerID, inv.ID, inv.BillToName, inv.BillToAddress, inv.BillToGSTIN, inv.BillToEmail,
			inv.BillToPhone, inv.PlaceOfSupply, inv.SellerStateCode, inv.PricesIncludeGST, inv.DueDate, inv.Notes,
			inv.TaxablePaise, inv.CGSTPaise, inv.SGSTPaise, inv.IGSTPaise, inv.RoundOffPaise, inv.TotalPaise)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotDraft
		}
		return writeLines(ctx, tx, s.OrgID, inv.ID, inv.Lines)
	})
}

// Issue numbers a draft and stores it as issued. prepare receives the
// invoice with its number and issue date, recomputes it from the seller
// snapshot and returns the PDF. The counter row is locked for the whole
// transaction, so numbers are gapless and unique.
func (r *Repository) Issue(ctx context.Context, s Scope, id int64, today time.Time, prefix string, by int64,
	prepare func(inv *Invoice) ([]byte, error)) (Invoice, error) {
	err := pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		inv, err := scan(tx.QueryRow(ctx,
			`SELECT `+columns+` FROM invoices WHERE `+scopeFilter+` AND id = $3 FOR UPDATE`, s.OrgID, s.OwnerID, id))
		if err != nil {
			return err
		}
		if inv.Status != StatusDraft {
			return ErrNotDraft
		}
		if inv.Lines, err = r.lines(ctx, tx, id); err != nil {
			return err
		}
		fy, short := financialYear(today)
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO invoice_counters (org_id, financial_year, last_sequence) VALUES ($1, $2, 1)
			ON CONFLICT (org_id, financial_year) DO UPDATE SET last_sequence = invoice_counters.last_sequence + 1
			RETURNING last_sequence`, s.OrgID, fy).Scan(&seq); err != nil {
			return err
		}
		number, err := invoiceNumber(prefix, short, seq)
		if err != nil {
			return fieldErr("invoice_prefix", "invalid", err.Error())
		}
		date := today.Format("2006-01-02")
		inv.Number, inv.FinancialYear, inv.IssueDate, inv.Status = &number, &fy, &date, StatusIssued
		pdf, err := prepare(&inv)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE invoices SET status = 'issued', number = $3, financial_year = $4, sequence = $5, issue_date = $6::date,
				due_date = $7::date, seller_name = $8, seller_gstin = $9, seller_state_code = $10, seller_address = $11,
				seller_bank_details = $12, seller_terms = $13, taxable_paise = $14, cgst_paise = $15, sgst_paise = $16,
				igst_paise = $17, round_off_paise = $18, total_paise = $19, pdf = $20, issued_at = now(),
				issued_by_id = $21, updated_at = now()
			WHERE org_id = $1 AND id = $2`,
			s.OrgID, id, number, fy, seq, date, inv.DueDate, inv.SellerName, inv.SellerGSTIN, inv.SellerStateCode,
			inv.SellerAddress, inv.sellerBankDetails, inv.sellerTerms, inv.TaxablePaise, inv.CGSTPaise, inv.SGSTPaise,
			inv.IGSTPaise, inv.RoundOffPaise, inv.TotalPaise, pdf, by,
		); err != nil {
			return err
		}
		return writeLines(ctx, tx, s.OrgID, id, inv.Lines)
	})
	if err != nil {
		return Invoice{}, err
	}
	return r.Get(ctx, s, id)
}

// PDF returns an issued invoice's stored PDF.
func (r *Repository) PDF(ctx context.Context, s Scope, id int64) ([]byte, error) {
	var pdf []byte
	err := r.db.QueryRow(ctx, `SELECT pdf FROM invoices WHERE `+scopeFilter+` AND id = $3`, s.OrgID, s.OwnerID, id).Scan(&pdf)
	return pdf, mapErr(err)
}

func (r *Repository) MarkSent(ctx context.Context, orgID, id int64) error {
	_, err := r.db.Exec(ctx, `UPDATE invoices SET sent_at = now(), updated_at = now() WHERE org_id = $1 AND id = $2`, orgID, id)
	return err
}

// Cancel cancels an issued invoice; the number stays used.
func (r *Repository) Cancel(ctx context.Context, s Scope, id int64, reason string) error {
	tag, err := r.db.Exec(ctx,
		`UPDATE invoices SET status = 'cancelled', cancelled_at = now(), cancel_reason = $4, updated_at = now()
		WHERE `+scopeFilter+` AND id = $3 AND status = 'issued'`, s.OrgID, s.OwnerID, id, reason)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotIssued
	}
	return err
}

// DeleteDraft removes a draft (issued invoices are cancelled, never deleted).
func (r *Repository) DeleteDraft(ctx context.Context, s Scope, id int64) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM invoices WHERE `+scopeFilter+` AND id = $3 AND status = 'draft'`,
		s.OrgID, s.OwnerID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotDraft
	}
	return err
}
