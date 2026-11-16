//go:build cgo

package ui

import (
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
)

// chatRoomMessages guarda en la sala name n mensajes de texto ("mensaje 00"…, o el de texts), uno
// por minuto desde base; los de images llevan una imagen.
func chatRoomMessages(t *testing.T, root, name string, n int, base time.Time, texts map[int]string, images ...int) []history.Message {
	t.Helper()
	store, err := history.New(root, name)
	if err != nil {
		t.Fatal(err)
	}
	isImage := map[int]bool{}
	for _, i := range images {
		isImage[i] = true
	}
	messages := make([]history.Message, n)
	for i := range messages {
		// ID a mano: NewID usa la hora, que en Windows se repite en un bucle así.
		m := history.Message{ID: fmt.Sprintf("%032x", i+1), Timestamp: base.Add(time.Duration(i) * time.Minute), SenderID: "p1", Username: "Pepe", Kind: "text", Text: fmt.Sprintf("mensaje %02d", i)}
		if text, ok := texts[i]; ok {
			m.Text = text
		}
		if isImage[i] {
			m.Kind, m.ImageID, m.ImageExt = "image", fmt.Sprintf("%032x", 1000+i), ".png"
			if _, err := store.SaveImage(m.ImageID, m.ImageExt, tinyPNG(t)); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Add(m); err != nil {
			t.Fatal(err)
		}
		messages[i] = m
	}
	return messages
}

// waitThumbs espera (las miniaturas se decodifican en otra goroutine) a que thumbCache tenga n.
func waitThumbs(t *testing.T, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for thumbCache.len() != n {
		if time.Now().After(deadline) {
			t.Fatalf("%s: %d miniaturas guardadas, quería %d", what, thumbCache.len(), n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestChatBuildsLastRows (C38): el chat arma solo los últimos maxChatRows mensajes, "Ver
// anteriores" suma de a maxChatRows, un mensaje nuevo saca la fila más vieja, el buscador busca en
// todo y arma hasta la coincidencia, y las miniaturas guardadas son solo las de las filas armadas.
func TestChatBuildsLastRows(t *testing.T) {
	a, root := newRoomTestApp(t)
	messages := chatRoomMessages(t, root, "Sala", 50, time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC), map[int]string{3: "el viejo"}, 5, 45, 48)

	rowsAre := func(what string, first, n int, hidden int) {
		t.Helper()
		if len(a.chatBox.Objects) != n || len(a.chatShown) != n {
			t.Fatalf("%s: %d filas (%d mensajes), quería %d", what, len(a.chatBox.Objects), len(a.chatShown), n)
		}
		if a.chatShown[0].ID != messages[first].ID {
			t.Fatalf("%s: la primera fila es %q, quería %q", what, a.chatShown[0].Text, messages[first].Text)
		}
		if hidden == 0 {
			if a.chatMore.Visible() {
				t.Fatalf("%s: se ve %q con todos armados", what, a.chatMore.Text)
			}
			return
		}
		if want := i18n.T("chat.show_older", hidden); !a.chatMore.Visible() || a.chatMore.Text != want {
			t.Fatalf("%s: el botón dice %q (visible %v), quería %q", what, a.chatMore.Text, a.chatMore.Visible(), want)
		}
	}

	a.initHistory("Sala")
	if len(a.chatMessages) != 50 {
		t.Fatalf("chat cargado: %d mensajes", len(a.chatMessages))
	}
	rowsAre("al entrar", 30, maxChatRows, 30)
	waitThumbs(t, 2, "al entrar") // las de 45 y 48; la de 5 no está armada

	test.Tap(a.chatMore)
	rowsAre("Ver anteriores", 10, 40, 10)
	test.Tap(a.chatMore)
	rowsAre("Ver anteriores dos veces", 0, 50, 0)
	waitThumbs(t, 3, "con todas las filas")

	a.hideToTray() // a la bandeja vuelve a las últimas 20
	rowsAre("en la bandeja", 30, maxChatRows, 30)
	waitThumbs(t, 2, "en la bandeja")
	a.showMainWindow()

	test.Tap(a.chatMore)
	a.viewRoom("Sala") // otra sala (o la misma, de nuevo): las últimas 20
	rowsAre("al cambiar de sala", 30, maxChatRows, 30)

	// Un mensaje nuevo con las 20 filas armadas: entra abajo y sale la más vieja.
	a.activeRoomName = "Sala"
	added := history.Message{ID: fmt.Sprintf("%032x", 2000), Timestamp: time.Now().UTC(), SenderID: "p2", Username: "Ana", Kind: "text", Text: "nuevo"}
	a.addChatMessage(added)
	rowsAre("mensaje nuevo", 31, maxChatRows, 31)
	if last := a.chatShown[maxChatRows-1]; last.ID != added.ID {
		t.Fatalf("la última fila es %q, quería el mensaje nuevo", last.Text)
	}
	var thumbs []*tappableImage // las de 45 y 48
	for _, row := range a.chatBox.Objects {
		walkObjects(row, func(o fyne.CanvasObject) {
			if thumb, ok := o.(*tappableImage); ok {
				thumbs = append(thumbs, thumb)
			}
		})
	}
	if len(thumbs) != 2 || thumbs[0].image.Image == nil {
		t.Fatalf("filas con imagen: %d", len(thumbs))
	}
	for i := range 15 { // la imagen de 45 sale de las filas: su miniatura también
		a.addChatMessage(history.Message{ID: fmt.Sprintf("%032x", 2001+i), Timestamp: time.Now().UTC(), SenderID: "p2", Username: "Ana", Kind: "text", Text: fmt.Sprintf("más %d", i)})
	}
	rowsAre("16 mensajes nuevos", 46, maxChatRows, 46)
	waitThumbs(t, 1, "con la imagen de 45 recortada")
	// Fyne guarda un rato la fila que salió: sin soltar sus píxeles, la memoria no bajaba.
	if thumbs[0].image.Image != nil || thumbs[1].image.Image == nil {
		t.Fatal("la fila recortada sigue con su imagen, o la que quedó la perdió")
	}

	// El buscador busca en todo: cuenta y arma hasta la coincidencia vieja, sin esconder el resto.
	a.chatSearch.SetText("VIEJO")
	if got := a.chatSearchCount.Text; got != "1/1" { // "actual/total" desde C40
		t.Fatalf("contador: %q", got)
	}
	if a.chatShown[0].ID != messages[3].ID || len(a.chatShown) != 63 {
		t.Fatalf("buscando: %d filas desde %q, quería 63 desde el viejo", len(a.chatShown), a.chatShown[0].Text)
	}
	a.chatSearch.SetText("")
	rowsAre("sin buscar", 46, maxChatRows, 46)

	a.closeRoomView()
	if n := thumbCache.len(); n != 0 || len(a.chatShown) != 0 || a.chatMore.Visible() {
		t.Fatalf("después de colgar: %d miniaturas, %d filas, botón visible %v", n, len(a.chatShown), a.chatMore.Visible())
	}
}

// TestShowOlderKeepsView (C38): "Ver anteriores" deja a la vista lo mismo que antes (las filas
// nuevas quedan arriba).
func TestShowOlderKeepsView(t *testing.T) {
	a, root := newRoomTestApp(t)
	chatRoomMessages(t, root, "Sala", 50, time.Date(2026, 11, 16, 20, 0, 0, 0, time.UTC), nil)
	a.initHistory("Sala")
	relayout(a.window)
	a.chatScroll.ScrollToTop()
	saveShot(t, a.window, "c38_ver_anteriores")
	top := func(row int) float32 { // dónde se ve la fila, respecto del scroll
		return a.chatBox.Position().Y + a.chatBox.Objects[row].Position().Y - a.chatScroll.Offset.Y
	}
	before := top(0) // el mensaje 30
	test.Tap(a.chatMore)
	relayout(a.window)
	if a.chatScroll.Offset.Y == 0 {
		t.Fatal("el scroll quedó arriba de todo")
	}
	if after := top(maxChatRows); after != before { // el mismo mensaje, con 20 filas más arriba
		t.Fatalf("la fila que estaba arriba quedó en %v, estaba en %v", after, before)
	}
}
