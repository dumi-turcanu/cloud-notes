package models_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/dumi-turcanu/cloud-notes/backend/internal/models"
)

func TestNote_RoundTripsJSON(t *testing.T) {
	note := models.Note{
		ID: uuid.New(), UserID: uuid.New(), ShareUUID: uuid.New(), Title: "t",
		Content: json.RawMessage(`{"body":"x"}`), CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	b, err := json.Marshal(note)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got models.Note
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ShareUUID != note.ShareUUID || got.UserID != note.UserID || !got.CreatedAt.Equal(note.CreatedAt) {
		t.Errorf("round trip mismatch: %+v vs %+v", got, note)
	}
}

func TestNote_JSONShape(t *testing.T) {
	b, _ := json.Marshal(models.Note{Title: "t", Content: json.RawMessage(`{}`)})
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"id", "user_id", "title", "content", "is_public", "share_uuid", "created_at", "updated_at", "deleted_at"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	for _, k := range []string{"last_synced_at", "link_metadata"} {
		if _, ok := m[k]; ok {
			t.Errorf("vestigial key %q must not be present", k)
		}
	}
}

func TestPublicNote_HasNoIdentifiers(t *testing.T) {
	b, _ := json.Marshal(models.PublicNote{Title: "t", Content: json.RawMessage(`{}`)})
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if len(m) != 3 {
		t.Errorf("want exactly title/content/updated_at, got %v", m)
	}
}
