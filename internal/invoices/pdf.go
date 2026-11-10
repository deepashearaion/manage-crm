package invoices

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/signintech/gopdf"
)

// Noto Sans (SIL Open Font License 1.1, see fonts/OFL.txt) renders ₹ and
// Latin-script names. Scripts it lacks (e.g. Devanagari) print as blanks.
var (
	//go:embed fonts/NotoSans-Regular.ttf
	fontRegular []byte
	//go:embed fonts/NotoSans-Bold.ttf
	fontBold []byte
)

const (
	pageW, pageH = 595.28, 841.89 // A4 in points
	margin       = 40.0
	contentW     = pageW - 2*margin
	bottomLimit  = pageH - 60
)

// column is a table column: header, width and right alignment.
type column struct {
	title string
	w     float64
	right bool
}

// pdfDoc wraps gopdf with a cursor and small layout helpers.
type pdfDoc struct {
	pdf *gopdf.GoPdf
	y   float64
	err error
}

func (d *pdfDoc) font(bold bool, size float64) {
	name := "regular"
	if bold {
		name = "bold"
	}
	if err := d.pdf.SetFont(name, "", size); err != nil && d.err == nil {
		d.err = err
	}
}

// text writes s in a box at (x, y) of width w; align is gopdf.Left,
// gopdf.Right or gopdf.Center.
func (d *pdfDoc) text(x, y, w float64, s string, align int) {
	d.pdf.SetXY(x, y)
	if err := d.pdf.CellWithOption(&gopdf.Rect{W: w, H: 12}, s, gopdf.CellOption{Align: align | gopdf.Top}); err != nil && d.err == nil {
		d.err = err
	}
}

// wrap splits s (which may hold newlines) into lines at most w wide.
func (d *pdfDoc) wrap(s string, w float64) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		lines, err := d.pdf.SplitTextWithWordWrap(para, w)
		if err != nil {
			// A word wider than the column: fall back to character wrapping.
			if lines, err = d.pdf.SplitText(para, w); err != nil {
				lines = []string{para}
			}
		}
		out = append(out, lines...)
	}
	return out
}

// paragraph writes wrapped text at x, advancing y by lineH per line.
func (d *pdfDoc) paragraph(x, w float64, s string, lineH float64) {
	for _, line := range d.wrap(s, w) {
		d.ensure(lineH)
		d.text(x, d.y, w, line, gopdf.Left)
		d.y += lineH
	}
}

// ensure starts a new page when h more points do not fit.
func (d *pdfDoc) ensure(h float64) bool {
	if d.y+h <= bottomLimit {
		return false
	}
	d.pdf.AddPage()
	d.y = margin
	return true
}

func (d *pdfDoc) rule(y float64) {
	d.pdf.SetStrokeColor(200, 200, 200)
	d.pdf.SetLineWidth(0.5)
	d.pdf.Line(margin, y, pageW-margin, y)
}

// renderPDF draws a tax invoice. A draft (no number yet) is marked as such.
func renderPDF(inv Invoice) ([]byte, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	if err := pdf.AddTTFFontData("regular", fontRegular); err != nil {
		return nil, err
	}
	if err := pdf.AddTTFFontData("bold", fontBold); err != nil {
		return nil, err
	}
	title := "Tax invoice"
	if inv.Number != nil {
		title += " " + *inv.Number
	}
	pdf.SetInfo(gopdf.PdfInfo{Title: title, Creator: "manage-crm"})
	pdf.AddPage()
	d := &pdfDoc{pdf: pdf, y: margin}
	half := contentW / 2

	// Title and invoice details (right), seller (left).
	d.font(true, 16)
	label := "TAX INVOICE"
	if inv.Status == StatusCancelled {
		label = "TAX INVOICE (CANCELLED)"
	}
	d.text(margin+half, d.y, half, label, gopdf.Right)
	if inv.Number == nil {
		d.font(true, 10)
		pdf.SetTextColor(200, 0, 0)
		d.text(margin+half, d.y+20, half, "DRAFT: not a valid tax invoice", gopdf.Right)
		pdf.SetTextColor(0, 0, 0)
	}
	d.font(true, 13)
	d.text(margin, d.y, half, inv.SellerName, gopdf.Left)
	top := d.y + 18

	d.y = top
	d.font(false, 9)
	d.paragraph(margin, half-10, inv.SellerAddress, 11)
	if inv.SellerGSTIN != "" {
		d.text(margin, d.y, half, "GSTIN: "+inv.SellerGSTIN, gopdf.Left)
		d.y += 11
	}
	if inv.SellerStateCode != "" {
		d.text(margin, d.y, half, "State: "+stateLabel(inv.SellerStateCode), gopdf.Left)
		d.y += 11
	}
	leftBottom := d.y

	d.y = top + 22
	details := [][2]string{}
	if inv.Number != nil {
		details = append(details, [2]string{"Invoice no.", *inv.Number})
	}
	if inv.IssueDate != nil {
		details = append(details, [2]string{"Invoice date", *inv.IssueDate})
	}
	if inv.DueDate != nil {
		details = append(details, [2]string{"Due date", *inv.DueDate})
	}
	details = append(details, [2]string{"Place of supply", stateLabel(inv.PlaceOfSupply)}, [2]string{"Reverse charge", "No"})
	for _, kv := range details {
		d.font(false, 9)
		d.text(margin+half, d.y, half-120, kv[0], gopdf.Left)
		d.font(true, 9)
		d.text(margin+half+80, d.y, half-80, kv[1], gopdf.Right)
		d.y += 12
	}
	d.y = max(d.y, leftBottom) + 10
	d.rule(d.y)
	d.y += 10

	// Bill to.
	d.font(true, 9)
	d.text(margin, d.y, contentW, "Bill to", gopdf.Left)
	d.y += 13
	d.font(true, 10)
	d.text(margin, d.y, contentW, inv.BillToName, gopdf.Left)
	d.y += 13
	d.font(false, 9)
	d.paragraph(margin, half, inv.BillToAddress, 11)
	for _, s := range []string{prefixed("GSTIN: ", inv.BillToGSTIN), joinNonEmpty(" · ", inv.BillToPhone, inv.BillToEmail)} {
		if s != "" {
			d.text(margin, d.y, contentW, s, gopdf.Left)
			d.y += 11
		}
	}
	d.y += 10

	// Lines.
	intra := inv.CGSTPaise+inv.SGSTPaise > 0 || (inv.IGSTPaise == 0 && inv.PlaceOfSupply == inv.SellerStateCode)
	taxTitle := "IGST"
	if intra {
		taxTitle = "CGST+SGST"
	}
	cols := []column{
		{"#", 16, false}, {"Description", 150, false}, {"SAC", 40, false}, {"Qty", 26, true},
		{"Rate", 62, true}, {"Taxable", 66, true}, {"GST", 30, true}, {taxTitle, 60, true}, {"Amount", 65, true},
	}
	header := func() {
		pdf.SetFillColor(238, 238, 238)
		pdf.RectFromUpperLeftWithStyle(margin, d.y, contentW, 16, "F")
		// gopdf draws text with the fill colour too: restore black.
		pdf.SetFillColor(0, 0, 0)
		pdf.SetTextColor(0, 0, 0)
		d.font(true, 8)
		x := margin
		for _, c := range cols {
			align := gopdf.Left
			if c.right {
				align = gopdf.Right
			}
			d.text(x+2, d.y+3, c.w-4, c.title, align)
			x += c.w
		}
		d.y += 20
	}
	header()
	for i, l := range inv.Lines {
		d.font(false, 8)
		desc := d.wrap(l.Description, cols[1].w-4)
		h := float64(len(desc))*10 + 6
		if d.ensure(h) {
			header()
			d.font(false, 8)
		}
		cells := []string{
			fmt.Sprint(i + 1), "", l.SAC, fmt.Sprint(l.Quantity), formatRupees(l.UnitPricePaise),
			formatRupees(l.TaxablePaise), formatRate(l.GSTRateBP), formatRupees(l.CGSTPaise + l.SGSTPaise + l.IGSTPaise),
			formatRupees(l.TotalPaise),
		}
		x := margin
		for j, c := range cols {
			if j == 1 {
				for k, line := range desc {
					d.text(x+2, d.y+float64(k)*10, c.w-4, line, gopdf.Left)
				}
			} else {
				align := gopdf.Left
				if c.right {
					align = gopdf.Right
				}
				d.text(x+2, d.y, c.w-4, cells[j], align)
			}
			x += c.w
		}
		d.y += h
		d.rule(d.y - 3)
	}
	d.y += 6

	// Totals.
	totals := [][2]string{{"Taxable value", formatRupees(inv.TaxablePaise)}}
	if intra {
		totals = append(totals, [2]string{"CGST", formatRupees(inv.CGSTPaise)}, [2]string{"SGST", formatRupees(inv.SGSTPaise)})
	} else {
		totals = append(totals, [2]string{"IGST", formatRupees(inv.IGSTPaise)})
	}
	if inv.RoundOffPaise != 0 {
		totals = append(totals, [2]string{"Round off", formatRupees(inv.RoundOffPaise)})
	}
	d.ensure(float64(len(totals)+1)*14 + 30)
	for _, kv := range totals {
		d.font(false, 9)
		d.text(pageW-margin-230, d.y, 140, kv[0], gopdf.Left)
		d.text(pageW-margin-90, d.y, 90, kv[1], gopdf.Right)
		d.y += 13
	}
	d.rule(d.y)
	d.y += 4
	d.font(true, 11)
	d.text(pageW-margin-230, d.y, 140, "Total", gopdf.Left)
	d.text(pageW-margin-120, d.y, 120, formatRupees(inv.TotalPaise), gopdf.Right)
	d.y += 20
	d.font(false, 9)
	d.paragraph(margin, contentW, "Amount in words: "+amountInWords(inv.TotalPaise), 11)
	if inv.PricesIncludeGST {
		d.paragraph(margin, contentW, "Prices include GST.", 11)
	}
	d.y += 8

	// Notes, bank details, terms.
	for _, block := range [][2]string{{"Notes", inv.Notes}, {"Bank details", inv.sellerBankDetails}, {"Terms", inv.sellerTerms}} {
		if strings.TrimSpace(block[1]) == "" {
			continue
		}
		d.ensure(30)
		d.font(true, 9)
		d.text(margin, d.y, contentW, block[0], gopdf.Left)
		d.y += 12
		d.font(false, 9)
		d.paragraph(margin, contentW, block[1], 11)
		d.y += 6
	}
	if inv.Status == StatusCancelled && inv.CancelReason != "" {
		d.font(true, 9)
		d.paragraph(margin, contentW, "Cancelled: "+inv.CancelReason, 11)
	}
	// Signatory (GST requires the supplier or an authorised representative).
	d.ensure(50)
	d.y += 10
	d.font(true, 9)
	d.text(pageW-margin-220, d.y, 220, "For "+inv.SellerName, gopdf.Right)
	d.y += 30
	d.font(false, 9)
	d.text(pageW-margin-220, d.y, 220, "Authorised signatory", gopdf.Right)

	d.font(false, 8)
	pdf.SetTextColor(110, 110, 110)
	d.text(margin, bottomLimit+20, contentW, "This is a computer-generated invoice.", gopdf.Center)

	if d.err != nil {
		return nil, d.err
	}
	return pdf.GetBytesPdf(), nil
}

func prefixed(p, s string) string {
	if s == "" {
		return ""
	}
	return p + s
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
