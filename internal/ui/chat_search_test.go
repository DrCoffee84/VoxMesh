//go:build cgo

package ui

import (
	"fmt"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"

	"voxmesh/internal/history"
	"voxmesh/internal/i18n"
)

// TestChatFilter (C40): el texto (en el mensaje o el nombre), el rango de días enteros en hora
// local (abierto de un lado, los bordes del día) y las dos condiciones juntas.
func TestChatFilter(t *testing.T) {
	at := func(day, hour, minute, second int) history.Message {
		return history.Message{Username: "Pepe", Text: "hola mundo", Timestamp: time.Date(2026, 11, day, hour, minute, second, 0, time.Local).UTC()}
	}
	// DateEntry da el día elegido a las 00:00 UTC.
	day := func(d int) *time.Time { v := time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC); return &v }
	cases := []struct {
		name     string
		filter   chatFilter
		m        history.Message
		expected bool
	}{
		{"sin nada buscado", rangeFilter("", nil, nil), at(15, 12, 0, 0), false},
		{"texto", rangeFilter("mundo", nil, nil), at(15, 12, 0, 0), true},
		{"nombre", rangeFilter("pepe", nil, nil), at(15, 12, 0, 0), true},
		{"otro texto", rangeFilter("chau", nil, nil), at(15, 12, 0, 0), false},
		{"desde: el comienzo del día", rangeFilter("", day(15), nil), at(15, 0, 0, 0), true},
		{"desde: un segundo antes", rangeFilter("", day(15), nil), at(14, 23, 59, 59), false},
		{"hasta: el final del día", rangeFilter("", nil, day(15)), at(15, 23, 59, 59), true},
		{"hasta: el día siguiente", rangeFilter("", nil, day(15)), at(16, 0, 0, 0), false},
		{"rango de un día", rangeFilter("", day(15), day(15)), at(15, 12, 0, 0), true},
		{"rango y texto", rangeFilter("mundo", day(15), day(16)), at(16, 8, 0, 0), true},
		{"rango sí, texto no", rangeFilter("chau", day(15), day(16)), at(16, 8, 0, 0), false},
		{"texto sí, rango no", rangeFilter("mundo", day(15), day(16)), at(17, 8, 0, 0), false},
	}
	for _, c := range cases {
		if got := c.filter.matches(c.m); got != c.expected {
			t.Errorf("%s: %v, quería %v", c.name, got, c.expected)
		}
	}
	visible := []history.Message{at(14, 9, 0, 0), at(15, 9, 0, 0), at(16, 9, 0, 0)}
	if got := chatMatchIndexes(visible, rangeFilter("", day(15), nil)); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("índices desde el 15: %v", got)
	}
}

// TestMoveMatch (C40): las flechas no dan la vuelta y avisan la punta (también si ya estaba).
func TestMoveMatch(t *testing.T) {
	cases := []struct {
		cur, n   int
		move     matchMove
		pos      int
		edge     bool
		scenario string
	}{
		{4, 5, matchPrev, 3, false, "▲ desde el más nuevo"},
		{1, 5, matchPrev, 0, true, "▲ llega al primero"},
		{0, 5, matchPrev, 0, true, "▲ en el primero no se mueve"},
		{3, 5, matchNext, 4, true, "▼ llega al último"},
		{4, 5, matchNext, 4, true, "▼ en el último no se mueve"},
		{2, 5, matchFirst, 0, true, "⏮"},
		{0, 5, matchFirst, 0, true, "⏮ ya en el primero"},
		{2, 5, matchLast, 4, true, "⏭"},
		{2, 5, matchNext, 3, false, "▼ en el medio"},
		{0, 1, matchNext, 0, true, "una sola coincidencia"},
	}
	for _, c := range cases {
		if pos, edge := moveMatch(c.cur, c.n, c.move); pos != c.pos || edge != c.edge {
			t.Errorf("%s: (%d, %v), quería (%d, %v)", c.scenario, pos, edge, c.pos, c.edge)
		}
	}
}

// TestChatSearchArrows (C40): con el driver de test, el contador "actual/total", la actual marcada
// y a la vista, las flechas y Enter, el destello en las puntas, una coincidencia fuera de las 20
// filas de C38 que se arma al ir a ella, el rango de fechas y que todo se limpia al cambiar de sala.
func TestChatSearchArrows(t *testing.T) {
	a, root := newRoomTestApp(t)
	setLanguage(t, "es")
	// Un mensaje por hora desde el 14/11 a las 00:30: del 24 al 47 son del 15.
	base := time.Date(2026, 11, 14, 0, 30, 0, 0, time.Local)
	texts := map[int]string{2: "la clave", 10: "otra clave", 30: "clave del 15", 45: "última clave"}
	store, err := history.New(root, "Sala")
	if err != nil {
		t.Fatal(err)
	}
	var messages []history.Message
	for i := range 50 {
		text, ok := texts[i]
		if !ok {
			text = "nada"
		}
		m := history.Message{ID: fmt.Sprintf("%032x", i+1), Timestamp: base.Add(time.Duration(i) * time.Hour).UTC(), SenderID: "p1", Username: "Pepe", Kind: "text", Text: text}
		if err := store.Add(m); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	a.initHistory("Sala")
	relayout(a.window)

	rowOf := func(i int) int { return a.chatRowOf(messages[i].ID) }
	expect := func(what, counter string, current int) {
		t.Helper()
		if got := a.chatSearchCount.Text; got != counter {
			t.Fatalf("%s: contador %q, quería %q", what, got, counter)
		}
		if a.chatCurrentID != messages[current].ID {
			t.Fatalf("%s: la actual no es el mensaje %d", what, current)
		}
		row := rowOf(current)
		if row < 0 {
			t.Fatalf("%s: la actual no está armada", what)
		}
		for i, marks := range a.chatMarks {
			if marked := marks.match != nil && marks.match.stroke == theme.ColorNamePrimary; marked != (i == row) {
				t.Fatalf("%s: la fila %d marcada=%v (la actual es la %d)", what, i, marked, row)
			}
		}
		top := a.chatBox.Position().Y + a.chatBox.Objects[row].Position().Y
		if offset := a.chatScroll.Offset.Y; top < offset || top+a.chatBox.Objects[row].Size().Height > offset+a.chatScroll.Size().Height {
			t.Fatalf("%s: la actual (y=%v) no está a la vista (scroll %v, alto %v)", what, top, offset, a.chatScroll.Size().Height)
		}
	}
	flashes := func(i int) int { return a.chatMarks[rowOf(i)].highlight.flashes }

	if a.chatNav.Visible() {
		t.Fatal("las flechas se ven sin nada buscado")
	}
	a.chatSearch.SetText("clave")
	if !a.chatNav.Visible() {
		t.Fatal("con algo buscado no se ven las flechas")
	}
	expect("al escribir", "4/4", 45)
	if flashes(45) != 0 {
		t.Fatal("al escribir destelló")
	}
	saveShot(t, a.window, "c40_buscador")

	test.Tap(arrowButton(a, 1)) // ▲
	expect("▲", "3/4", 30)
	if flashes(30) != 0 {
		t.Fatal("▲ en el medio destelló")
	}
	a.chatSearch.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn}) // Enter = ▲: el 10, fuera de las 20 filas
	expect("Enter", "2/4", 10)
	if a.chatShown[0].ID != messages[10].ID {
		t.Fatalf("ir al 10 armó desde %q", a.chatShown[0].Text)
	}
	test.Tap(arrowButton(a, 0)) // ⏮
	expect("⏮", "1/4", 2)
	if flashes(2) != 1 {
		t.Fatalf("⏮ al primero: %d destellos", flashes(2))
	}
	test.Tap(arrowButton(a, 1)) // ▲ en el primero: no se mueve y vuelve a destellar
	expect("▲ en el primero", "1/4", 2)
	if flashes(2) != 2 {
		t.Fatalf("▲ en el primero: %d destellos", flashes(2))
	}
	test.Tap(arrowButton(a, 2)) // ▼
	expect("▼", "2/4", 10)
	test.Tap(arrowButton(a, 3)) // ⏭
	expect("⏭", "4/4", 45)
	test.Tap(arrowButton(a, 2)) // ▼ en el último
	expect("▼ en el último", "4/4", 45)
	if flashes(45) != 2 {
		t.Fatalf("⏭ y ▼ en el último: %d destellos", flashes(45))
	}

	// Rango de fechas: el 15, con el texto (las dos condiciones) y sin él (todo el día).
	a.showChatRange()
	if !a.chatRangePopup.Visible() {
		t.Fatal("no se abrió el rango de fechas")
	}
	fifteenth := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	a.chatFromEntry.SetDate(&fifteenth) // como al elegirla en el calendario
	a.chatToEntry.SetDate(&fifteenth)
	expect("clave el 15", "2/2", 45) // el 30 y el 45
	if a.chatRangeBtn.Icon != chatRangeOnIcon || a.chatRangeBtn.hint != "Fechas: del 15/11/2026 al 15/11/2026" {
		t.Fatalf("botón del rango: resaltado=%v leyenda %q", a.chatRangeBtn.Icon == chatRangeOnIcon, a.chatRangeBtn.hint)
	}
	saveShot(t, a.window, "c40_rango")
	a.chatRangePopup.Hide()
	a.chatSearch.SetText("")
	expect("todo el 15", "24/24", 47)
	a.chatToEntry.SetDate(nil) // abierto: desde el 15
	expect("desde el 15", "26/26", 49)

	// Cambiar de sala (o colgar) saca el rango y la actual, y vuelve a las últimas 20.
	a.viewRoom("Sala")
	if a.chatRangeFrom != nil || a.chatRangeTo != nil || a.chatFromEntry.Date != nil || a.chatRangeBtn.Icon != chatRangeIcon || a.chatRangeBtn.hint != i18n.T("chat.range_hint") {
		t.Fatal("el rango sigue después de cambiar de sala")
	}
	if a.chatNav.Visible() || a.chatSearchCount.Text != "" || len(a.chatShown) != maxChatRows {
		t.Fatalf("después de cambiar de sala: flechas %v, contador %q, %d filas", a.chatNav.Visible(), a.chatSearchCount.Text, len(a.chatShown))
	}
}

// arrowButton es la flecha i del buscador (⏮ ▲ ▼ ⏭).
func arrowButton(a *App, i int) *hintButton {
	arrows := a.chatNav.Objects[1].(*fyne.Container) // Border: el contador al centro y las flechas a la derecha
	return arrows.Objects[i].(*hintButton)
}
