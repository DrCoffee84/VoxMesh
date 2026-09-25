package history

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreDeduplicatesMessagesAndReportsMissing(t *testing.T) {
	store, err := New(t.TempDir(), "gaming")
	if err != nil {
		t.Fatal(err)
	}
	message := Message{ID: "message-1", Kind: "text", Text: "hola"}
	if err := store.Add(message); err != nil {
		t.Fatal(err)
	}
	if err := store.Add(message); err != nil {
		t.Fatal(err)
	}
	if got := store.KnownIDs(); len(got) != 1 || got[0] != message.ID {
		t.Fatalf("unexpected known IDs: %#v", got)
	}
	missing := store.Missing([]Message{message, {ID: "message-2", Kind: "text"}})
	if len(missing) != 1 || missing[0].ID != "message-2" {
		t.Fatalf("unexpected missing messages: %#v", missing)
	}
}

func TestStoreSavesImageIdempotently(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, "gaming")
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.SaveImage("image-1", ".png", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := store.SaveImage("image-1", ".png", []byte("other"))
	if err != nil {
		t.Fatal(err)
	}
	if path != secondPath || !store.HasImage("image-1", ".png") {
		t.Fatalf("image was not stored idempotently: %q, %q", path, secondPath)
	}
	data, err := os.ReadFile(filepath.Join(root, "salas", "gaming", "images", "image-1.png"))
	if err != nil || string(data) != "data" {
		t.Fatalf("stored image changed unexpectedly: %q, %v", data, err)
	}
}

func TestStoreSavesAndDeletesSound(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, "gaming")
	if err != nil {
		t.Fatal(err)
	}
	meta := SoundMeta{ID: "snd-1", Ext: ".pcm", Name: "Aplauso"}
	_, err = store.SaveSound(meta, []byte("pcmdata"))
	if err != nil {
		t.Fatal(err)
	}
	sounds, err := store.ListSounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(sounds) != 1 || sounds[0].ID != "snd-1" {
		t.Fatalf("expected 1 sound, got: %#v", sounds)
	}
	if err := store.DeleteSound("snd-1", ".pcm"); err != nil {
		t.Fatal(err)
	}
	soundsAfter, err := store.ListSounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(soundsAfter) != 0 {
		t.Fatalf("expected 0 sounds after delete, got: %#v", soundsAfter)
	}
}
