package httpx

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 7807 error body, used by every /api/v1 endpoint. Code is
// a stable machine-readable identifier; Errors maps request fields to what
// is wrong with them.
type Problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Code      string            `json:"code"`
	Errors    map[string]string `json:"errors,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// WriteProblem answers with an application/problem+json error.
func WriteProblem(w http.ResponseWriter, r *http.Request, status int, code, detail string) {
	WriteProblemFields(w, r, status, code, detail, nil)
}

// WriteProblemFields is WriteProblem with per-field validation errors.
func WriteProblemFields(w http.ResponseWriter, r *http.Request, status int, code, detail string, fields map[string]string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(Problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		Code:      code,
		Errors:    fields,
		RequestID: RequestIDFrom(r.Context()),
	})
}

// DecodeJSON decodes a request body strictly (unknown fields are errors), as
// /api/v1 endpoints do, answering a 400 problem when it fails.
func DecodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		WriteProblem(w, r, http.StatusBadRequest, "invalid_body", "The request body is not valid JSON for this endpoint: "+err.Error())
		return false
	}
	return true
}
