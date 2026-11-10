package invoices

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func sampleInvoice(lines int) Invoice {
	num, date := "INV/26-27/0001", "2026-11-10"
	inv := Invoice{
		Status: StatusIssued, Number: &num, IssueDate: &date, DueDate: &date,
		SellerName: "Shearaion Travels Pvt Ltd", SellerGSTIN: "27AAPFU0939F1ZV", SellerStateCode: "27",
		SellerAddress: "12 MG Road\nPune 411001", BillToName: "Priya Sharma", BillToEmail: "priya@example.com",
		BillToAddress: "4 Lake View, Bengaluru", PlaceOfSupply: "29", Notes: "Thank you for booking with us. ₹ prices.",
		sellerBankDetails: "HDFC Bank, A/c 1234567890, IFSC HDFC0000123", sellerTerms: "Payable within 7 days.",
	}
	for i := range lines {
		inv.Lines = append(inv.Lines, Line{
			Description: strings.Repeat("Goa holiday package, 4 nights, 2 adults, airport transfers ", 1+i%3),
			SAC:         "998552", Quantity: 1 + i%3, UnitPricePaise: 4500000, GSTRateBP: 500,
		})
	}
	computeTotals(&inv)
	return inv
}

func TestRenderPDF(t *testing.T) {
	for _, n := range []int{1, 60} { // 60 lines spill onto more pages
		pdf, err := renderPDF(sampleInvoice(n))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(pdf, []byte("%PDF-")) || len(pdf) < 1000 {
			t.Fatalf("not a PDF (%d bytes)", len(pdf))
		}
		if pages := bytes.Count(pdf, []byte("/Type /Page\n")) + bytes.Count(pdf, []byte("/Type /Page ")); n == 60 && pages < 2 {
			t.Logf("page markers found: %d", pages)
		}
		if out := os.Getenv("INVOICE_PDF_OUT"); out != "" && n == 1 {
			_ = os.WriteFile(out, pdf, 0o600) // for eyeballing the layout
		}
	}

	draft := sampleInvoice(2)
	draft.Number, draft.Status = nil, StatusDraft
	if _, err := renderPDF(draft); err != nil {
		t.Fatal(err)
	}
}
