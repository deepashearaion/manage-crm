package money

import "testing"

func TestFormat(t *testing.T) {
	for paise, want := range map[int64]string{0: "₹0.00", 5: "₹0.05", 123456: "₹1,234.56", 1234567890: "₹1,23,45,678.90", -100000: "-₹1,000.00"} {
		if got := Format(paise); got != want {
			t.Errorf("Format(%d) = %q, want %q", paise, got, want)
		}
	}
}
