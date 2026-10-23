package query

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSingleQueryPreservesResultPresence(t *testing.T) {
	for _, tt := range []struct {
		body string
		ok   bool
	}{{`{"result":null}`, false}, {`{}`, false}, {`{"result":0}`, true}} {
		t.Run(tt.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
			defer server.Close()
			value, err := QueryUrl[uint64](server.URL, "test", Params{})
			if tt.ok {
				if err != nil || value == nil || *value != 0 {
					t.Fatalf("valid zero: %v %v", value, err)
				}
			} else if err == nil || value != nil {
				t.Fatalf("missing result became success: %v %v", value, err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":[]}`)) }))
	defer server.Close()
	value, err := QueryUrl[[]string](server.URL, "test", Params{})
	if err != nil || value == nil || len(*value) != 0 {
		t.Fatalf("empty array: %v %v", value, err)
	}
}
