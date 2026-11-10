package invoices

import (
	"testing"
	"time"
)

func TestValidGSTIN(t *testing.T) {
	for g, want := range map[string]bool{
		"27AAPFU0939F1ZV": true,  // well-known valid sample
		"27AAPFU0939F1ZW": false, // wrong check digit
		"99AAPFU0939F1ZV": false, // no such state
		"27aapfu0939f1zv": false,
		"27AAPFU0939F1Z":  false,
		"":                false,
	} {
		if got := ValidGSTIN(g); got != want {
			t.Errorf("ValidGSTIN(%q) = %v", g, got)
		}
	}
}

func TestComputeLine(t *testing.T) {
	// Exclusive, intra-state: ₹1,000 × 2 at 18% → 360 tax, split 180/180.
	l := Line{Quantity: 2, UnitPricePaise: 100000, GSTRateBP: 1800}
	computeLine(&l, false, true)
	if l.TaxablePaise != 200000 || l.CGSTPaise != 18000 || l.SGSTPaise != 18000 || l.IGSTPaise != 0 || l.TotalPaise != 236000 {
		t.Errorf("exclusive intra = %+v", l)
	}
	// Inclusive, inter-state: ₹1,180 incl. 18% → 1,000 + 180 IGST.
	l = Line{Quantity: 1, UnitPricePaise: 118000, GSTRateBP: 1800}
	computeLine(&l, true, false)
	if l.TaxablePaise != 100000 || l.IGSTPaise != 18000 || l.TotalPaise != 118000 {
		t.Errorf("inclusive inter = %+v", l)
	}
	// Odd tax paisa goes to SGST: 5% of ₹0.99 = 4.95 → 5 paise → 2 + 3.
	l = Line{Quantity: 1, UnitPricePaise: 99, GSTRateBP: 500}
	computeLine(&l, false, true)
	if l.CGSTPaise != 2 || l.SGSTPaise != 3 {
		t.Errorf("odd paisa = %+v", l)
	}
}

func TestComputeTotals(t *testing.T) {
	inv := Invoice{PlaceOfSupply: "29", SellerStateCode: "29", Lines: []Line{
		{Quantity: 1, UnitPricePaise: 1234567, GSTRateBP: 500},
		{Quantity: 3, UnitPricePaise: 33333, GSTRateBP: 1800},
	}}
	computeTotals(&inv)
	// 12345.67 + 5% = 617.28(35) → 617.28; 999.99 + 18% = 180.00 (179.9982) → 180.00
	if inv.TaxablePaise != 1334566 || inv.CGSTPaise+inv.SGSTPaise != 61728+18000 || inv.IGSTPaise != 0 {
		t.Fatalf("totals = %+v", inv)
	}
	exact := int64(1334566 + 61728 + 18000) // 14142.94
	if inv.TotalPaise != 1414300 || inv.RoundOffPaise != 1414300-exact {
		t.Errorf("total %d round-off %d", inv.TotalPaise, inv.RoundOffPaise)
	}
	inv.PlaceOfSupply = "27"
	computeTotals(&inv)
	if inv.IGSTPaise != 61728+18000 || inv.CGSTPaise != 0 {
		t.Errorf("inter-state totals = %+v", inv)
	}
}

func TestFinancialYearAndNumber(t *testing.T) {
	for day, want := range map[string]string{"2026-03-31": "2025-26", "2026-04-01": "2026-27", "2026-11-10": "2026-27", "2099-12-01": "2099-00"} {
		d, _ := time.Parse("2006-01-02", day)
		if got, _ := financialYear(d); got != want {
			t.Errorf("financialYear(%s) = %s", day, got)
		}
	}
	if n, err := invoiceNumber("INV", "26-27", 1); err != nil || n != "INV/26-27/0001" {
		t.Errorf("number = %q %v", n, err)
	}
	if _, err := invoiceNumber("ABCDE", "26-27", 100000); err == nil {
		t.Error("17-character number accepted")
	}
}

func TestWordsAndFormatting(t *testing.T) {
	for paise, want := range map[int64]string{
		0:            "Rupees Zero Only",
		100:          "Rupees One Only",
		1414300:      "Rupees Fourteen Thousand One Hundred Forty Three Only",
		10050050:     "Rupees One Lakh Five Hundred and Fifty Paise Only",
		123456789012: "Rupees One Hundred Twenty Three Crore Forty Five Lakh Sixty Seven Thousand Eight Hundred Ninety and Twelve Paise Only",
	} {
		if got := amountInWords(paise); got != want {
			t.Errorf("amountInWords(%d) = %q", paise, got)
		}
	}
	for paise, want := range map[int64]string{5: "₹0.05", 123456: "₹1,234.56", 1234567890: "₹1,23,45,678.90", -100000: "-₹1,000.00"} {
		if got := formatRupees(paise); got != want {
			t.Errorf("formatRupees(%d) = %q", paise, got)
		}
	}
	if formatRate(1800) != "18%" || formatRate(250) != "2.5%" || formatRate(25) != "0.25%" {
		t.Errorf("rates %s %s %s", formatRate(1800), formatRate(250), formatRate(25))
	}
}
