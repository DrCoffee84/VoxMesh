package history

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// IDs con el formato de NewID (32 hex): el Store rechaza cualquier otro.
const (
	imageID = "00112233445566778899aabbccddeeff"
	soundID = "ffeeddccbbaa99887766554433221100"
)

func TestStoreRejectsUnsafeAssetNames(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	store, err := New(root, "gaming")
	if err != nil {
		t.Fatal(err)
	}
	// images/<id> + "/../../../../../" sube hasta base: ahí queda el archivo que no se debe tocar.
	secret := filepath.Join(base, "secret.png")
	if err := os.WriteFile(secret, []byte("secreto"), 0600); err != nil {
		t.Fatal(err)
	}
	badExts := []string{
		".exe", ".png.exe", "/../../../../../secret.png", `\..\..\..\..\..\secret.png`,
		".png/../../x.png", `C:\Windows\x.png`, ".png:stream", ".png ", "..", ".",
	}
	badIDs := []string{"", "../x", "..", "AABBCCDDEEFF00112233445566778899", imageID[:31], imageID + "0", "con"}

	check := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: se esperaba error", what)
		}
	}
	try := func(id, ext string) {
		t.Helper()
		_, err := store.SaveImage(id, ext, []byte("x"))
		check(fmt.Sprintf("SaveImage(%q, %q)", id, ext), err)
		_, err = store.ReadImage(id, ext)
		check(fmt.Sprintf("ReadImage(%q, %q)", id, ext), err)
		_, err = store.ImagePath(id, ext)
		check(fmt.Sprintf("ImagePath(%q, %q)", id, ext), err)
		if store.HasImage(id, ext) {
			t.Errorf("HasImage(%q, %q) = true", id, ext)
		}
		_, err = store.SaveSound(SoundMeta{ID: id, Ext: ext, Name: "x"}, []byte("x"))
		check(fmt.Sprintf("SaveSound(%q, %q)", id, ext), err)
		_, err = store.ReadSound(id, ext)
		check(fmt.Sprintf("ReadSound(%q, %q)", id, ext), err)
		_, err = store.SoundPath(id, ext)
		check(fmt.Sprintf("SoundPath(%q, %q)", id, ext), err)
		check(fmt.Sprintf("DeleteSound(%q, %q)", id, ext), store.DeleteSound(id, ext))
		check(fmt.Sprintf("UpdateSoundMeta(%q, %q)", id, ext), store.UpdateSoundMeta(SoundMeta{ID: id, Ext: ext, Name: "x"}))
	}
	for _, ext := range badExts {
		try(imageID, ext)
	}
	for _, id := range badIDs {
		try(id, ".png")
		try(id, ".pcm")
	}
	if _, err := store.SaveImage(imageID, "", []byte("x")); err == nil {
		t.Error("SaveImage sin extensión: se esperaba error")
	}

	if data, err := os.ReadFile(secret); err != nil || string(data) != "secreto" {
		t.Fatalf("el archivo de afuera cambió: %q, %v", data, err)
	}
	// Ninguna llamada fue válida: no tiene que haber ningún archivo, ni dentro ni fuera de la sala.
	_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && path != secret {
			t.Errorf("se creó un archivo: %s", path)
		}
		return err
	})
	if sounds, err := store.ListSounds(); err != nil || len(sounds) != 0 {
		t.Fatalf("el manifest quedó con entradas: %#v, %v", sounds, err)
	}
	if got := store.DeletedSounds(); len(got) != 0 {
		t.Fatalf("quedaron lápidas de IDs inválidos: %#v", got)
	}
}

func TestStoreNormalizesExtensions(t *testing.T) {
	root := t.TempDir()
	store, err := New(root, "gaming")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveImage(imageID, ".PNG", []byte("data")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "salas", "gaming", "images", imageID+".png")); err != nil {
		t.Fatalf("la imagen no quedó con la extensión en minúsculas: %v", err)
	}
	if data, err := store.ReadImage(imageID, ".png"); err != nil || string(data) != "data" {
		t.Fatalf("ReadImage: %q, %v", data, err)
	}
	if _, err := store.SaveSound(SoundMeta{ID: soundID, Ext: ".PCM", Name: "a"}, []byte("pcm")); err != nil {
		t.Fatal(err)
	}
	const wavID = "0123456789abcdef0123456789abcdef"
	if _, err := store.SaveSound(SoundMeta{ID: wavID, Name: "b"}, []byte("wav")); err != nil {
		t.Fatal(err)
	}
	if data, err := store.ReadSound(wavID, ""); err != nil || string(data) != "wav" {
		t.Fatalf("ReadSound sin extensión: %q, %v", data, err)
	}
	sounds, err := store.ListSounds()
	if err != nil || len(sounds) != 2 || sounds[0].Ext != ".pcm" || sounds[1].Ext != ".wav" {
		t.Fatalf("manifest inesperado: %#v, %v", sounds, err)
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		"Mi sala":       "Mi sala",
		"  gaming  ":    "gaming",
		"a/b\\c":        "a_b_c",
		`a<b>:c"d|e?f*`: "a_b__c_d_e_f_",
		"a\x00b\nc":     "a_b_c",
		"":              "sala",
		".":             "sala",
		"..":            "sala",
		"sala. .":       "sala",
		"CON":           "CON_",
		"con.txt":       "con_.txt",
		"Nul":           "Nul_",
		"COM1":          "COM1_",
		"lpt9.x.y":      "lpt9_.x.y",
		"COM10":         "COM10",
		"CONSOLA":       "CONSOLA",
		"../../x":       ".._.._x",
	}
	for in, want := range cases {
		got := safeName(in)
		if got != want {
			t.Errorf("safeName(%q) = %q, se esperaba %q", in, got, want)
		}
		if again := safeName(got); again != got {
			t.Errorf("safeName no es idempotente: %q → %q → %q", in, got, again)
		}
	}
}

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
	path, err := store.SaveImage(imageID, ".png", []byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := store.SaveImage(imageID, ".png", []byte("other"))
	if err != nil {
		t.Fatal(err)
	}
	if path != secondPath || !store.HasImage(imageID, ".png") {
		t.Fatalf("image was not stored idempotently: %q, %q", path, secondPath)
	}
	data, err := os.ReadFile(filepath.Join(root, "salas", "gaming", "images", imageID+".png"))
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
	meta := SoundMeta{ID: soundID, Ext: ".pcm", Name: "Aplauso"}
	_, err = store.SaveSound(meta, []byte("pcmdata"))
	if err != nil {
		t.Fatal(err)
	}
	sounds, err := store.ListSounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(sounds) != 1 || sounds[0].ID != soundID {
		t.Fatalf("expected 1 sound, got: %#v", sounds)
	}
	if err := store.DeleteSound(soundID, ".pcm"); err != nil {
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

func TestStoreKeepsSoundTombstones(t *testing.T) {
	store, err := New(t.TempDir(), "gaming")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSound(SoundMeta{ID: soundID, Ext: ".pcm", Name: "Aplauso"}, []byte("pcmdata")); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSound(soundID, ".pcm"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddSoundTombstones([]string{"snd-2", soundID, ""}); err != nil {
		t.Fatal(err)
	}
	got := store.DeletedSounds()
	if len(got) != 2 || got[0] != soundID || got[1] != "snd-2" {
		t.Fatalf("unexpected tombstones: %#v", got)
	}
}

func TestStoreTrimsSoundTombstones(t *testing.T) {
	store, err := New(t.TempDir(), "gaming")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, MaxSoundTombstones+10)
	for i := range ids {
		ids[i] = fmt.Sprintf("snd-%d", i)
	}
	if err := store.AddSoundTombstones(ids); err != nil {
		t.Fatal(err)
	}
	got := store.DeletedSounds()
	if len(got) != MaxSoundTombstones || got[0] != "snd-10" || got[len(got)-1] != ids[len(ids)-1] {
		t.Fatalf("expected the newest %d tombstones, got %d starting at %q", MaxSoundTombstones, len(got), got[0])
	}
	if again := store.DeletedSounds(); len(again) != MaxSoundTombstones {
		t.Fatalf("trimmed file read back %d tombstones", len(again))
	}
}

func TestStoreRenamesSound(t *testing.T) {
	store, err := New(t.TempDir(), "gaming")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSound(SoundMeta{ID: soundID, Ext: ".pcm", Name: "Aplauso"}, []byte("pcmdata")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateSoundMeta(SoundMeta{ID: soundID, Ext: ".pcm", Name: "Ovación"}); err != nil {
		t.Fatal(err)
	}
	sounds, err := store.ListSounds()
	if err != nil {
		t.Fatal(err)
	}
	if len(sounds) != 1 || sounds[0].Name != "Ovación" {
		t.Fatalf("expected the renamed sound only, got: %#v", sounds)
	}
	if err := store.UpdateSoundMeta(SoundMeta{ID: "0123456789abcdef0123456789abcdef", Ext: ".pcm", Name: "X"}); err == nil {
		t.Fatal("expected an error for a sound that is not on disk")
	}
}
