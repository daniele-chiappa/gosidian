package mcp

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/auth"
)

// oversizedMultipart is a body that exceeds the attachment cap by a wide
// margin: the parser must refuse it up front (413) instead of spooling it to
// a temp file before the size check runs (BUG-034).
func oversizedMultipart(t *testing.T, field string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, "huge.bin")
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < int(attach.MaxBytes>>20)+3; i++ {
		if _, err := fw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestHTTPUpload_OversizedBodyIs413(t *testing.T) {
	s, plaintext := serverWithToken(t, "", []string{auth.ScopeRead, auth.ScopeWrite})
	body, ct := oversizedMultipart(t, "file")
	r := httptest.NewRequest(http.MethodPost, "/upload?project=proj", body)
	r.Header.Set("Authorization", "Bearer "+plaintext)
	r.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.handleHTTPUpload(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestIngestTicketRedeem_OversizedBodyIs413(t *testing.T) {
	s, ctx := newScopedServer(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	out := ingestOut(t, s, ctx, map[string]any{"project": "proj", "transfer": "http"})
	endpoint, _ := out["endpoint"].(string)
	huge := bytes.Repeat([]byte("x"), int(attach.MaxBytes)+3<<20)
	code, body := postTicket(t, s.Handler(""), endpoint, "huge.bin", huge)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", code, body)
	}
}
