package comments

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"serve/internal/store"
)

func TestImportV1(t *testing.T) {
	s, p := setup(t)
	old := filepath.Join(t.TempDir(), "comments")
	os.MkdirAll(old, 0o755)
	pid := func(s string) *string { return &s }
	file := map[string]any{
		"path": p,
		"comments": []map[string]any{
			{"id": "r1", "text": "Why three?", "created_at": "2026-09-01T10:00:00Z", "anchor_text": "up to three times",
				"block_text": "We retry failed card payments up to three times over five days."},
			{"id": "a1", "text": "Changed it.", "created_at": "2026-09-01T10:05:00Z", "parent_id": pid("r1")},
			{"id": "h1", "text": "Thanks", "created_at": "2026-09-01T10:06:00Z", "parent_id": pid("a1"), "anchor_text": "up to three times"},
			{"id": "pg", "text": "Overall fine", "created_at": "2026-09-01T11:00:00Z", "scope": "page", "resolved": true},
			{"id": "or", "text": "orphan reply", "created_at": "2026-09-01T12:00:00Z", "parent_id": pid("missing")},
			{"id": "gone", "text": "text is gone", "created_at": "2026-09-01T13:00:00Z", "anchor_text": "words that are not there",
				"block_text": "A paragraph that was deleted entirely from the spec."},
		},
	}
	b, _ := json.Marshal(file)
	os.WriteFile(filepath.Join(old, "abcd.json"), b, 0o644)
	os.WriteFile(filepath.Join(old, "legacy.json"), []byte(`[{"id":"x","text":"no path"}]`), 0o644)

	res, err := s.ImportV1(old)
	if err != nil || res == nil {
		t.Fatalf("import: %v %v", res, err)
	}
	if res.Threads != 4 || res.Messages != 6 || res.Placed != 1 {
		t.Fatalf("result %+v", res)
	}
	again, _ := s.ImportV1(old)
	if again != nil {
		t.Fatalf("second import ran")
	}
	dv, _ := s.Open(p, false)
	var main ThreadView
	for _, v := range dv.Threads {
		if v.Messages[0].Body == "Why three?" {
			main = v
		}
	}
	if len(main.Messages) != 3 {
		t.Fatalf("nested replies not flattened: %d", len(main.Messages))
	}
	kinds := []string{main.Messages[0].Author.Kind, main.Messages[1].Author.Kind, main.Messages[2].Author.Kind}
	if kinds[0] != store.Human || kinds[1] != store.Agent || kinds[2] != store.Human {
		t.Fatalf("authors %v", kinds)
	}
	if main.Location.State != LocOK || main.Location.LineStart != 5 {
		t.Fatalf("imported anchor %+v", main.Location)
	}
	for _, v := range dv.Threads {
		switch v.Messages[0].Body {
		case "orphan reply":
			if v.Scope != store.ScopePage {
				t.Errorf("orphan reply scope %q", v.Scope)
			}
		case "text is gone":
			if v.Location.State != LocUnplaced {
				t.Errorf("gone text state %q", v.Location.State)
			}
		case "Overall fine":
			if v.Status != store.StatusResolved {
				t.Errorf("resolved flag lost")
			}
		}
	}
}
