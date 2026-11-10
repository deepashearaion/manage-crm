package leads

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	body := []byte(`{"object":"whatsapp_business_account"}`)
	good := sign("app-secret", body)
	if !validSignature("app-secret", body, good) {
		t.Error("valid signature rejected")
	}
	for _, header := range []string{"", good[7:], sign("other", body), "sha256=zz", "sha1=" + good[7:]} {
		if validSignature("app-secret", body, header) {
			t.Errorf("signature %q accepted", header)
		}
	}
	if validSignature("app-secret", append(body, ' '), good) {
		t.Error("signature accepted for a different body")
	}
}

func TestParseWebhook(t *testing.T) {
	body := []byte(`{
	  "object": "whatsapp_business_account",
	  "entry": [{"id": "WABA", "changes": [{
	    "field": "messages",
	    "value": {
	      "messaging_product": "whatsapp",
	      "metadata": {"display_phone_number": "15550001111", "phone_number_id": "1234567890"},
	      "contacts": [{"profile": {"name": "Priya Sharma"}, "wa_id": "919876543210"}],
	      "messages": [
	        {"from": "919876543210", "id": "wamid.A", "timestamp": "1762761600", "type": "text", "text": {"body": "Price for Goa?"}},
	        {"from": "919876543210", "id": "wamid.B", "timestamp": "1762761660", "type": "image", "image": {"caption": "this one"}},
	        {"from": "919876543210", "id": "wamid.C", "timestamp": "1762761700", "type": "reaction", "reaction": {"emoji": "👍"}},
	        {"from": "919876543210", "id": "wamid.D", "timestamp": "1762761720", "type": "interactive",
	         "interactive": {"type": "button_reply", "button_reply": {"id": "yes", "title": "Yes, call me"}}}
	      ]
	    }
	  }, {
	    "field": "messages",
	    "value": {"metadata": {"phone_number_id": "1234567890"},
	              "statuses": [{"id": "wamid.X", "status": "delivered"}]}
	  }]}]
	}`)
	msgs, err := parseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	m := msgs[0]
	if m.PhoneNumberID != "1234567890" || m.WaID != "919876543210" || m.Name != "Priya Sharma" ||
		m.ID != "wamid.A" || m.Body != "Price for Goa?" || !m.At.Equal(time.Unix(1762761600, 0)) {
		t.Errorf("text message = %+v", m)
	}
	if msgs[1].Body != "[image] this one" || msgs[2].Body != "Yes, call me" {
		t.Errorf("image / button reply = %q / %q", msgs[1].Body, msgs[2].Body)
	}

	if _, err := parseWebhook([]byte(`{"object":"page"}`)); err == nil {
		t.Error("non-WhatsApp notification accepted")
	}
}

func TestSplitName(t *testing.T) {
	for _, tc := range []struct{ name, first, last string }{
		{"Priya Sharma", "Priya", "Sharma"},
		{"  Priya   K  Sharma ", "Priya", "K Sharma"},
		{"Priya", "Priya", ""},
		{"", "+919876543210", ""},
	} {
		first, last := splitName(tc.name, "919876543210")
		if first != tc.first || last != tc.last {
			t.Errorf("splitName(%q) = %q, %q", tc.name, first, last)
		}
	}
}
