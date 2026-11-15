//go:build cgo

package ui

import (
	"bytes"
	"image"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/config"
	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
	"voxmesh/internal/room"
	"voxmesh/internal/transport"
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

// newRoomTestApp: App con ventana y las salas en una carpeta temporal (config.AppDir).
func newRoomTestApp(t *testing.T) (*App, string) {
	t.Helper()
	t.Setenv("AppData", t.TempDir())
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	a.window = test.NewTempWindow(t, a.content())
	a.window.Resize(fyne.NewSize(1000, 700))
	root, err := config.AppDir()
	if err != nil {
		t.Fatal(err)
	}
	return a, root
}

// saveRoom crea la sala name con un mensaje de chat, un sonido y, si withImage, una imagen.
func saveRoom(t *testing.T, root, name string, withImage bool) history.Message {
	t.Helper()
	store, err := history.New(root, name)
	if err != nil {
		t.Fatal(err)
	}
	message := history.Message{ID: history.NewID("p1"), Timestamp: time.Now().UTC(), SenderID: "p1", Username: "Pepe", Kind: "text", Text: "hola " + name}
	if withImage {
		message.Kind, message.Text = "image", ""
		message.ImageID, message.ImageExt = history.NewID("img"), ".png"
		if _, err := store.SaveImage(message.ImageID, message.ImageExt, tinyPNG(t)); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Add(message); err != nil {
		t.Fatal(err)
	}
	sound := history.SoundMeta{ID: history.NewID("p1"), Ext: ".pcm", Name: "Bocina"}
	if _, err := store.SaveSound(sound, make([]byte, 960)); err != nil {
		t.Fatal(err)
	}
	return message
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// walkWithScrolls es walkObjects que además entra en el contenido de los container.Scroll.
func walkWithScrolls(object fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	walkObjects(object, func(o fyne.CanvasObject) {
		visit(o)
		if scroll, ok := o.(*container.Scroll); ok {
			walkWithScrolls(scroll.Content, visit)
		}
	})
}

// labelTexts junta los textos de los Label que hay en object.
func labelTexts(object fyne.CanvasObject) []string {
	var texts []string
	walkWithScrolls(object, func(o fyne.CanvasObject) {
		if label, ok := o.(*widget.Label); ok {
			texts = append(texts, label.Text)
		}
	})
	return texts
}

// TestCloseRoomViewLeavesRoom (C31): al colgar, la ventana queda como al abrir la app: sin
// historial, sin chat ni botonera, "Ver otra sala…" sin elegir y el texto de cómo se entra. La
// sala sigue guardada.
func TestCloseRoomViewLeavesRoom(t *testing.T) {
	setLanguage(t, "es")
	a, root := newRoomTestApp(t)
	saveRoom(t, root, "Sala", false)

	a.initHistory("Sala")
	if a.historyStore.Load() == nil || a.roomSelect.Selected != "Sala" || len(a.chatMessages) != 1 || len(a.sounds) != 1 {
		t.Fatalf("en la sala: store=%v elegida=%q chat=%d sonidos=%d", a.historyStore.Load() != nil, a.roomSelect.Selected, len(a.chatMessages), len(a.sounds))
	}
	if contains(labelTexts(a.chatBox), i18n.T("chat.no_room")) {
		t.Fatal("en la sala, el chat dice que no estás en ninguna")
	}

	a.closeRoomView()
	if a.historyStore.Load() != nil || a.viewStore.Load() != nil || a.viewingRoomName != "" {
		t.Fatal("después de colgar quedó abierta la carpeta de la sala")
	}
	if len(a.chatMessages) != 0 || len(a.sounds) != 0 {
		t.Fatalf("después de colgar: chat=%d sonidos=%d", len(a.chatMessages), len(a.sounds))
	}
	if a.roomSelect.Selected != "" || !contains(a.roomSelect.Options, "Sala") {
		t.Fatalf("Ver otra sala…: elegida %q, opciones %v", a.roomSelect.Selected, a.roomSelect.Options)
	}
	if got := labelTexts(a.chatBox); len(a.chatBox.Objects) != 1 || !contains(got, i18n.T("chat.no_room")) {
		t.Fatalf("chat fuera de la sala: %v", got)
	}
	if got := labelTexts(a.soundGrid); !contains(got, i18n.T("sound.no_room")) {
		t.Fatalf("botonera fuera de la sala: %v", got)
	}

	a.viewRoom("Sala") // "Ver otra sala…" sigue mostrando el chat guardado
	if len(a.chatMessages) != 1 || contains(labelTexts(a.chatBox), i18n.T("chat.no_room")) {
		t.Fatalf("mirar la sala guardada: chat=%d", len(a.chatMessages))
	}
}

// TestViewRoomShowsItsImages: "Ver otra sala…" muestra las imágenes guardadas en esa sala, estando
// en otra o fuera de una (antes se buscaban en la carpeta de la sala activa).
func TestViewRoomShowsItsImages(t *testing.T) {
	a, root := newRoomTestApp(t)
	saveRoom(t, root, "Sala", false)
	imageMessage := saveRoom(t, root, "Otra", true)
	hasThumb := func() bool {
		found := false
		walkObjects(a.buildChatMessageObject(imageMessage, false), func(o fyne.CanvasObject) {
			if _, ok := o.(*tappableImage); ok {
				found = true
			}
		})
		return found
	}

	a.initHistory("Sala")
	a.viewRoom("Otra")
	if !hasThumb() {
		t.Fatal("en una sala, mirando otra: sin la miniatura")
	}
	a.closeRoomView()
	a.viewRoom("Otra")
	if !hasThumb() {
		t.Fatal("fuera de la sala, mirando una guardada: sin la miniatura")
	}
}

// TestLateRoomStateAfterHangup (C31): un RoomState que llega después de colgar (la conexión ya no
// es la actual) no vuelve a abrir la sala; con la conexión actual, sí.
func TestLateRoomStateAfterHangup(t *testing.T) {
	a, root := newRoomTestApp(t)
	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	a.participantID = "yo"
	state := room.State{Name: "De Juan", HostID: "juan", Participants: []room.Participant{{ID: "juan", Username: "Juan", Connected: true}}}

	a.handleRoomState(state, udp)
	if a.activeRoomName != "" || a.historyStore.Load() != nil {
		t.Fatalf("estado tardío: abrió la sala %q", a.activeRoomName)
	}
	if rooms, _ := history.ListRooms(root); contains(rooms, "De Juan") {
		t.Fatal("estado tardío: creó la carpeta de la sala")
	}

	a.transport.Store(udp)
	defer a.transport.Store(nil)
	a.handleRoomState(state, udp)
	if a.activeRoomName != "De Juan" || a.historyStore.Load() == nil {
		t.Fatalf("con la conexión actual: sala %q", a.activeRoomName)
	}
}

// Un aviso de la sala viaja como clave + datos: cada uno lo lee en su idioma. Text, en el idioma
// del host, queda de respaldo para una clave que no conoce o que no es de avisos.
func TestSystemMessageInReceiverLanguage(t *testing.T) {
	setLanguage(t, "en") // el host
	sent := newSystemMessage("chat.sys.joined", "Ana")
	if sent.Text != "Ana joined." || !isSystemMessage(sent) || sent.Key != "chat.sys.joined" {
		t.Fatalf("aviso del host: %+v", sent)
	}
	payload, err := room.Encode(room.Envelope{Kind: room.SystemMessage, Message: &sent})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := room.Decode(payload)
	if err != nil || envelope.Message == nil {
		t.Fatalf("decode: %v", err)
	}
	i18n.Set("es") // el cliente
	if got := systemMessageText(*envelope.Message); got != "Ana se conectó." {
		t.Fatalf("cliente en español: %q", got)
	}
	for _, key := range []string{"", "chat.sys.no_existe", "settings.title"} {
		received := *envelope.Message
		received.Key = key
		if got := systemMessageText(received); got != "Ana joined." {
			t.Fatalf("clave %q: %q, quería el Text del host", key, got)
		}
	}
}

// TestDeleteSavedRoom (C30): borra la carpeta de la sala; si el chat la estaba mostrando, vuelve
// a "no estás en ninguna sala"; la sala de la llamada no se borra.
func TestDeleteSavedRoom(t *testing.T) {
	setLanguage(t, "es")
	a, root := newRoomTestApp(t)
	saveRoom(t, root, "Sala", false)
	saveRoom(t, root, "Otra", true)

	a.viewRoom("Otra") // fuera de la sala, mirando su chat
	if err := a.deleteSavedRoom("Otra"); err != nil {
		t.Fatal(err)
	}
	if rooms, _ := history.ListRooms(root); len(rooms) != 1 || rooms[0] != "Sala" {
		t.Fatalf("salas que quedan: %v", rooms)
	}
	if a.viewingRoomName != "" || a.viewStore.Load() != nil || !contains(labelTexts(a.chatBox), i18n.T("chat.no_room")) {
		t.Fatalf("el chat sigue mostrando la sala borrada (%q)", a.viewingRoomName)
	}
	if contains(a.roomSelect.Options, "Otra") {
		t.Fatalf("Ver otra sala… todavía la ofrece: %v", a.roomSelect.Options)
	}

	key, err := transport.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	udp, err := transport.Listen("127.0.0.1:0", key)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	a.initHistory("Sala")
	a.transport.Store(udp) // en la llamada
	defer a.transport.Store(nil)
	if err := a.deleteSavedRoom("Sala"); err == nil {
		t.Fatal("se borró la sala de la llamada")
	}
	if rooms, _ := history.ListRooms(root); !contains(rooms, "Sala") || a.historyStore.Load() == nil {
		t.Fatalf("la sala de la llamada: %v", rooms)
	}
}

// TestStartRoomDialogDeletesRoom (C30): "Iniciar sala" lista las salas guardadas con Abrir y el
// tacho; el tacho pide confirmación, borra la sala y la saca de la lista. Con salas guardadas,
// la entrada de una nueva arranca vacía.
func TestStartRoomDialogDeletesRoom(t *testing.T) {
	setLanguage(t, "es")
	a, root := newRoomTestApp(t)
	saveRoom(t, root, "Sala", false)
	saveRoom(t, root, "Otra", false)

	a.createHost()
	dialogContent := func() fyne.CanvasObject {
		pop, ok := a.window.Canvas().Overlays().Top().(*widget.PopUp)
		if !ok {
			t.Fatal("no hay un diálogo abierto")
		}
		return pop.Content
	}
	start := dialogContent()
	var opens, trashes []*widget.Button
	var entries []*widget.Entry
	walkWithScrolls(start, func(o fyne.CanvasObject) {
		switch w := o.(type) {
		case *widget.Button:
			if w.Text == i18n.T("conn.open") {
				opens = append(opens, w)
			}
			if w.Text == "" && w.Icon != nil && w.Icon.Name() == theme.DeleteIcon().Name() {
				trashes = append(trashes, w)
			}
		case *widget.Entry:
			entries = append(entries, w)
		}
	})
	labels := labelTexts(start)
	if len(opens) != 2 || len(trashes) != 2 || !contains(labels, "Sala") || !contains(labels, "Otra") {
		t.Fatalf("lista: %d Abrir, %d tachos, textos %v", len(opens), len(trashes), labels)
	}
	if len(entries) != 1 || entries[0].Text != "" {
		t.Fatalf("la entrada de una sala nueva tendría que arrancar vacía: %+v", entries)
	}

	test.Tap(trashes[0]) // "Otra" (orden alfabético)
	var confirmBtn *widget.Button
	walkObjects(dialogContent(), func(o fyne.CanvasObject) {
		if b, ok := o.(*widget.Button); ok && b.Text == i18n.T("conn.delete_room") {
			confirmBtn = b
		}
	})
	if confirmBtn == nil {
		t.Fatal("el tacho no pidió confirmación")
	}
	test.Tap(confirmBtn)

	deadline := time.Now().Add(3 * time.Second)
	for {
		rooms, _ := history.ListRooms(root)
		left := labelTexts(dialogContent())
		if len(rooms) == 1 && rooms[0] == "Sala" && !contains(left, "Otra") && contains(left, "Sala") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("después de borrar: salas %v, diálogo %v", rooms, left)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
