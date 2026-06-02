package main

import (
	"testing"
)

func ptr(s string) *string { return &s }

func findEvent(events []map[string]any, kind, id string) map[string]any {
	for _, e := range events {
		if e["event"] == kind && e["comment_id"] == id {
			return e
		}
	}
	return nil
}

func TestDiff_NewComment(t *testing.T) {
	next := []Comment{{ID: "a", Text: "hello", AnchorText: "anchor"}}
	events := diffSnapshots(nil, next, "/abs/doc.md")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0]["event"] != "new_comment" {
		t.Fatalf("expected new_comment, got %v", events[0]["event"])
	}
	if events[0]["text"] != "hello" {
		t.Fatalf("expected text=hello, got %v", events[0]["text"])
	}
	if events[0]["file"] != "/abs/doc.md" {
		t.Fatalf("expected file path in event")
	}
}

func TestDiff_NewReply(t *testing.T) {
	next := []Comment{{ID: "b", Text: "reply", ParentID: ptr("a")}}
	events := diffSnapshots(nil, next, "/abs/doc.md")
	if len(events) != 1 || events[0]["event"] != "new_reply" {
		t.Fatalf("expected new_reply, got %v", events)
	}
}

func TestDiff_Edited(t *testing.T) {
	prev := []Comment{{ID: "a", Text: "old"}}
	next := []Comment{{ID: "a", Text: "new"}}
	events := diffSnapshots(prev, next, "/abs/doc.md")
	if len(events) != 1 || events[0]["event"] != "edited" {
		t.Fatalf("expected edited, got %v", events)
	}
	if events[0]["text"] != "new" {
		t.Fatalf("expected text=new, got %v", events[0]["text"])
	}
}

func TestDiff_Resolved(t *testing.T) {
	prev := []Comment{{ID: "a", Text: "t"}}
	next := []Comment{{ID: "a", Text: "t", Resolved: true}}
	events := diffSnapshots(prev, next, "/abs/doc.md")
	if len(events) != 1 || events[0]["event"] != "resolved" {
		t.Fatalf("expected resolved, got %v", events)
	}
}

func TestDiff_Unresolved(t *testing.T) {
	prev := []Comment{{ID: "a", Text: "t", Resolved: true}}
	next := []Comment{{ID: "a", Text: "t"}}
	events := diffSnapshots(prev, next, "/abs/doc.md")
	if len(events) != 1 || events[0]["event"] != "unresolved" {
		t.Fatalf("expected unresolved, got %v", events)
	}
}

func TestDiff_Deleted(t *testing.T) {
	prev := []Comment{{ID: "a", Text: "gone"}}
	events := diffSnapshots(prev, nil, "/abs/doc.md")
	if len(events) != 1 || events[0]["event"] != "deleted" {
		t.Fatalf("expected deleted, got %v", events)
	}
}

func TestDiff_EditAndResolveSameComment(t *testing.T) {
	prev := []Comment{{ID: "a", Text: "old"}}
	next := []Comment{{ID: "a", Text: "new", Resolved: true}}
	events := diffSnapshots(prev, next, "/abs/doc.md")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), events)
	}
	if findEvent(events, "edited", "a") == nil {
		t.Fatal("missing edited event")
	}
	if findEvent(events, "resolved", "a") == nil {
		t.Fatal("missing resolved event")
	}
}

func TestDiff_NoChange(t *testing.T) {
	c := Comment{ID: "a", Text: "same"}
	events := diffSnapshots([]Comment{c}, []Comment{c}, "/abs/doc.md")
	if len(events) != 0 {
		t.Fatalf("expected no events, got %v", events)
	}
}

func TestDiff_MultipleNewAndDeleted(t *testing.T) {
	prev := []Comment{{ID: "x", Text: "old1"}, {ID: "y", Text: "old2"}}
	next := []Comment{{ID: "y", Text: "old2"}, {ID: "z", Text: "new"}}
	events := diffSnapshots(prev, next, "/abs/doc.md")
	if findEvent(events, "new_comment", "z") == nil {
		t.Fatal("missing new_comment for z")
	}
	if findEvent(events, "deleted", "x") == nil {
		t.Fatal("missing deleted for x")
	}
}
