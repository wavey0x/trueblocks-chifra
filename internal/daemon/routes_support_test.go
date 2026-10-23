package daemonPkg

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/usage"
)

type routeErrorResponse struct {
	Errors []string `json:"errors"`
}

func TestRespondWithRouteErrorUsageError(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondWithRouteError(rec, usage.Usage("bad option {0}", "--count"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	var body routeErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse error response %q: %v", rec.Body.String(), err)
	}
	if len(body.Errors) != 1 || body.Errors[0] != "bad option --count" {
		t.Fatalf("unexpected errors body: %#v", body.Errors)
	}
}

func TestRespondWithRouteErrorInternalError(t *testing.T) {
	rec := httptest.NewRecorder()

	RespondWithRouteError(rec, errors.New("database failed"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	var body routeErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("failed to parse error response %q: %v", rec.Body.String(), err)
	}
	if len(body.Errors) != 1 || body.Errors[0] != "database failed" {
		t.Fatalf("unexpected errors body: %#v", body.Errors)
	}
}
