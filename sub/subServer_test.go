package sub

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"
)

// The operator's custom headers ride on the raw subscription, GET and HEAD
// alike, and the whole sub server answers gzip to a client that asks for it.
// Driven through the real router, so the middleware order is what is tested.
func TestSubscriptionCustomHeadersAndGzip(t *testing.T) {
	setupSubDB(t)
	db := database.GetDB()
	if err := db.Create(&model.Client{
		Enable: true, Name: "gz",
		Config:   json.RawMessage(`{}`),
		Inbounds: json.RawMessage(`[]`),
		Links:    json.RawMessage(`[{"type":"external","remark":"x","uri":"trojan://pw@e.com:443#n"}]`),
	}).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	// Stored as if from before Save checked it: the line naming a header the
	// panel sets itself is skipped, the others are served.
	if err := db.Create(&model.Setting{
		Key:   "subCustomHeaders",
		Value: "announce: hello\nProfile-Title: hijacked\nannounce-url: https://e.com/",
	}).Error; err != nil {
		t.Fatalf("seed setting: %v", err)
	}

	engine, err := NewServer().initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	serve := func(method, target string, acceptGzip bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		if acceptGzip {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		return w
	}

	plain := serve("GET", "/sub/gz", false)
	if plain.Code != 200 {
		t.Fatalf("GET = %d, want 200: %s", plain.Code, plain.Body)
	}
	if got := plain.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q without Accept-Encoding, want none", got)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := serve(method, "/sub/gz", false)
		if got := w.Header().Get("announce"); got != "hello" {
			t.Errorf("%s: announce = %q, want %q", method, got, "hello")
		}
		if got := w.Header().Get("announce-url"); got != "https://e.com/" {
			t.Errorf("%s: announce-url = %q", method, got)
		}
		if got := w.Header().Get("Profile-Title"); got == "hijacked" || got == "" {
			t.Errorf("%s: Profile-Title = %q, want the panel's own", method, got)
		}
	}

	zipped := serve("GET", "/sub/gz", true)
	if got := zipped.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q with Accept-Encoding: gzip, want gzip", got)
	}
	zr, err := gzip.NewReader(zipped.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if string(body) != plain.Body.String() {
		t.Errorf("gunzipped body %q, want the plain one %q", body, plain.Body.String())
	}
	if got := zipped.Header().Get("announce"); got != "hello" {
		t.Errorf("gzip: announce = %q, want it kept", got)
	}

	// The dashboard is for people, not proxy clients.
	if got := serve("GET", "/sub/gz?format=page", false).Header().Get("announce"); got != "" {
		t.Errorf("dashboard page carries announce = %q, want none", got)
	}
}
