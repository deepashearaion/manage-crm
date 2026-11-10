package invoices

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/deepashearaion/manage-crm/internal/platform/money"
)

// ist is India Standard Time (UTC+5:30): invoice dates and financial years
// are IST.
var ist = time.FixedZone("IST", 5*3600+1800)

// States are the GST state codes (the first two digits of a GSTIN), used
// for the seller's state and the place of supply. 96 is used for supplies
// outside India and 97 for other territories.
var States = map[string]string{
	"01": "Jammu and Kashmir", "02": "Himachal Pradesh", "03": "Punjab", "04": "Chandigarh",
	"05": "Uttarakhand", "06": "Haryana", "07": "Delhi", "08": "Rajasthan", "09": "Uttar Pradesh",
	"10": "Bihar", "11": "Sikkim", "12": "Arunachal Pradesh", "13": "Nagaland", "14": "Manipur",
	"15": "Mizoram", "16": "Tripura", "17": "Meghalaya", "18": "Assam", "19": "West Bengal",
	"20": "Jharkhand", "21": "Odisha", "22": "Chhattisgarh", "23": "Madhya Pradesh", "24": "Gujarat",
	"26": "Dadra and Nagar Haveli and Daman and Diu", "27": "Maharashtra", "29": "Karnataka", "30": "Goa",
	"31": "Lakshadweep", "32": "Kerala", "33": "Tamil Nadu", "34": "Puducherry",
	"35": "Andaman and Nicobar Islands", "36": "Telangana", "37": "Andhra Pradesh", "38": "Ladakh",
	"96": "Outside India", "97": "Other Territory",
}

func stateLabel(code string) string {
	if name, ok := States[code]; ok {
		return code + " - " + name
	}
	return code
}

var gstinPattern = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

const gstinChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// ValidGSTIN checks a GSTIN's format, state code and check digit.
func ValidGSTIN(g string) bool {
	if !gstinPattern.MatchString(g) {
		return false
	}
	if _, ok := States[g[:2]]; !ok {
		return false
	}
	sum := 0
	for i := 0; i < 14; i++ {
		v := strings.IndexByte(gstinChars, g[i])
		p := v * (1 + i%2) // factors alternate 1, 2
		sum += p/36 + p%36
	}
	return gstinChars[(36-sum%36)%36] == g[14]
}

var sacPattern = regexp.MustCompile(`^[0-9]{4,8}$`)

// roundDiv divides non-negative a by b, rounding half up.
func roundDiv(a, b int64) int64 { return (a + b/2) / b }

// computeLine fills a line's tax and totals. With inclusive prices the
// unit price includes GST and the taxable value is backed out of it.
// Intra-state supplies split the tax into CGST and SGST (SGST takes the odd
// paisa); inter-state supplies charge IGST.
func computeLine(l *Line, inclusive, intraState bool) {
	gross := int64(l.Quantity) * l.UnitPricePaise
	rate := int64(l.GSTRateBP)
	var tax int64
	if inclusive {
		l.TaxablePaise = roundDiv(gross*10000, 10000+rate)
		tax = gross - l.TaxablePaise
	} else {
		l.TaxablePaise = gross
		tax = roundDiv(gross*rate, 10000)
	}
	l.CGSTPaise, l.SGSTPaise, l.IGSTPaise = 0, 0, 0
	if intraState {
		l.CGSTPaise = tax / 2
		l.SGSTPaise = tax - l.CGSTPaise
	} else {
		l.IGSTPaise = tax
	}
	l.TotalPaise = l.TaxablePaise + tax
}

// computeTotals computes every line and the invoice totals, rounding the
// grand total to the nearest rupee (the difference is the round-off).
func computeTotals(inv *Invoice) {
	intra := inv.PlaceOfSupply != "" && inv.PlaceOfSupply == inv.SellerStateCode
	inv.TaxablePaise, inv.CGSTPaise, inv.SGSTPaise, inv.IGSTPaise = 0, 0, 0, 0
	for i := range inv.Lines {
		computeLine(&inv.Lines[i], inv.PricesIncludeGST, intra)
		l := inv.Lines[i]
		inv.TaxablePaise += l.TaxablePaise
		inv.CGSTPaise += l.CGSTPaise
		inv.SGSTPaise += l.SGSTPaise
		inv.IGSTPaise += l.IGSTPaise
	}
	exact := inv.TaxablePaise + inv.CGSTPaise + inv.SGSTPaise + inv.IGSTPaise
	inv.TotalPaise = roundDiv(exact, 100) * 100
	inv.RoundOffPaise = inv.TotalPaise - exact
}

// financialYear returns the Indian financial year (April to March) of day,
// as "2026-27", and its short form "26-27" used in invoice numbers.
func financialYear(day time.Time) (string, string) {
	start := day.Year()
	if day.Month() < time.April {
		start--
	}
	return fmt.Sprintf("%d-%02d", start, (start+1)%100), fmt.Sprintf("%02d-%02d", start%100, (start+1)%100)
}

// invoiceNumber formats e.g. INV/26-27/0001. GST allows at most 16
// characters.
func invoiceNumber(prefix, shortFY string, seq int) (string, error) {
	n := fmt.Sprintf("%s/%s/%04d", prefix, shortFY, seq)
	if len(n) > 16 {
		return "", fmt.Errorf("invoice number %q is longer than 16 characters; use a shorter prefix", n)
	}
	return n, nil
}

var (
	ones = []string{"", "One", "Two", "Three", "Four", "Five", "Six", "Seven", "Eight", "Nine", "Ten",
		"Eleven", "Twelve", "Thirteen", "Fourteen", "Fifteen", "Sixteen", "Seventeen", "Eighteen", "Nineteen"}
	tens = []string{"", "", "Twenty", "Thirty", "Forty", "Fifty", "Sixty", "Seventy", "Eighty", "Ninety"}
)

// below100 spells 1-99.
func below100(n int64) string {
	if n < 20 {
		return ones[n]
	}
	if n%10 == 0 {
		return tens[n/10]
	}
	return tens[n/10] + " " + ones[n%10]
}

// indianWords spells n in the Indian system (crore, lakh, thousand).
func indianWords(n int64) string {
	if n == 0 {
		return "Zero"
	}
	var parts []string
	if n >= 10000000 {
		parts = append(parts, indianWords(n/10000000)+" Crore")
		n %= 10000000
	}
	for _, u := range []struct {
		div  int64
		name string
	}{{100000, "Lakh"}, {1000, "Thousand"}, {100, "Hundred"}} {
		if n >= u.div {
			parts = append(parts, below100(n/u.div)+" "+u.name)
			n %= u.div
		}
	}
	if n > 0 {
		parts = append(parts, below100(n))
	}
	return strings.Join(parts, " ")
}

// amountInWords spells an amount in paise, e.g. "Rupees One Lakh Five
// Hundred and Fifty Paise Only".
func amountInWords(paise int64) string {
	s := "Rupees " + indianWords(paise/100)
	if p := paise % 100; p > 0 {
		s += " and " + below100(p) + " Paise"
	}
	return s + " Only"
}

// formatRupees formats paise as ₹1,23,456.78.
func formatRupees(paise int64) string { return money.Format(paise) }

// formatRate formats basis points as a percentage: 1800 → 18%, 250 → 2.5%.
func formatRate(bp int) string {
	if bp%100 == 0 {
		return fmt.Sprintf("%d%%", bp/100)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(bp)/100), "0"), ".") + "%"
}
