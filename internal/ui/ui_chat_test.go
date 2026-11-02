//go:build cgo

package ui

import (
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
)

// TestActivityLogIsSessionOnly: el registro de actividad es solo de la sesión. Los avisos viejos
// que quedaron en el historial (de antes del 29/09) no se cargan al entrar a la sala, no se
// mandan en la sincronización y, si llegan por ella, se ignoran. El chat sí se carga.
func TestActivityLogIsSessionOnly(t *testing.T) {
	t.Setenv("AppData", t.TempDir()) // config.AppDir: las salas de la prueba, no las de la PC
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	test.NewTempWindow(t, a.content())

	root, err := config.AppDir()
	if err != nil {
		t.Fatal(err)
	}
	store, err := history.New(root, "Sala")
	if err != nil {
		t.Fatal(err)
	}
	old := history.Message{ID: history.NewID("system"), Timestamp: time.Date(2026, 9, 28, 23, 26, 0, 0, time.UTC), Username: "SISTEMA", Kind: "system", Text: "Robochot se conectó.", System: true}
	chat := history.Message{ID: history.NewID("p1"), Timestamp: time.Date(2026, 10, 26, 1, 39, 0, 0, time.UTC), SenderID: "p1", Username: "Pepe", Kind: "text", Text: "hola"}
	for _, message := range []history.Message{old, chat} {
		if err := store.Add(message); err != nil {
			t.Fatal(err)
		}
	}

	a.initHistory("Sala")
	if n := len(a.activityBox.Objects); n != 0 {
		t.Fatalf("entrar a la sala cargó %d avisos viejos al registro", n)
	}
	if len(a.chatMessages) != 1 || a.chatMessages[0].ID != chat.ID {
		t.Fatalf("chat cargado: %+v", a.chatMessages)
	}

	a.addChatMessage(old) // como llega en una respuesta de sincronización
	if n := len(a.activityBox.Objects); n != 0 {
		t.Fatalf("un aviso de la sincronización fue al registro (%d entradas)", n)
	}
	if got := chatMessagesOnly([]history.Message{old, chat}); len(got) != 1 || got[0].ID != chat.ID {
		t.Fatalf("la sincronización manda %+v, quería solo el chat", got)
	}

	a.addActivity("Pepe se conectó.") // en vivo, sí
	if n := len(a.activityBox.Objects); n != 1 {
		t.Fatalf("aviso en vivo: %d entradas, quería 1", n)
	}
}
