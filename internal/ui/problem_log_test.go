//go:build cgo

package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"voxmesh/internal/audio"
	"voxmesh/internal/i18n"
)

// TestProblemLine: cada problema trae su ayuda (en los dos idiomas) detrás del mismo texto de la
// barra de estado, separados por un espacio.
func TestProblemLine(t *testing.T) {
	for kind := problemDevice; kind <= problemWrongKey; kind++ {
		help, ok := problemHelp[kind]
		if !ok || !i18n.Has(help) {
			t.Errorf("problema %d sin ayuda en el catálogo (%q)", kind, help)
		}
	}
	missing := i18n.M("audio.input_missing", "Auriculares USB")
	for _, tc := range []struct {
		lang i18n.Lang
		want string
	}{
		{i18n.ES, "No encontré el micrófono «Auriculares USB»: uso el predeterminado de Windows. Si lo querés usar, conectalo y tocá Reiniciar audio, o elegí otro en Ajustes."},
		{i18n.EN, "Couldn't find the mic «Auriculares USB»: using the Windows default. To use it, plug it in and click Restart audio, or pick another one in Settings."},
	} {
		if got := problemLine(problemDevice, missing).In(tc.lang); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.lang, got, tc.want)
		}
	}
	both := sentences(i18n.M("audio.input_missing", "A"), i18n.M("audio.output_missing", "B"))
	if got := both.In(i18n.ES); got != "No encontré el micrófono «A»: uso el predeterminado de Windows. No encontré la salida «B»: uso la predeterminada de Windows." {
		t.Errorf("dos dispositivos: %q", got)
	}
	if got := problemLine(problemWrongKey, i18n.M("conn.wrong_key")).In(i18n.ES); got != "Alguien intenta entrar con una invitación vieja o de otra sala. Si es de los tuyos, pasale una nueva con «Invitar»." {
		t.Errorf("invitación vieja: %q", got)
	}
}

// TestProblemCap: la primera vez siempre; después, como mucho una cada problemLineCooldown por tipo.
func TestProblemCap(t *testing.T) {
	var c problemCap
	t0 := time.Date(2026, 11, 13, 21, 0, 0, 0, time.Local)
	steps := []struct {
		kind problemKind
		at   time.Duration
		want bool
	}{
		{problemSlowMic, 0, true},            // primera vez
		{problemSlowMic, time.Minute, false}, // dentro del tope
		{problemBusyPC, time.Minute, true},   // otro tipo: tope propio
		{problemSlowMic, 4*time.Minute + 59*time.Second, false},
		{problemSlowMic, 5 * time.Minute, true},  // se cumplió
		{problemSlowMic, 6 * time.Minute, false}, // cuenta desde la última línea
		{problemBusyPC, 6 * time.Minute, true},   // 5 min desde la suya
	}
	for i, step := range steps {
		if got := c.allow(step.kind, t0.Add(step.at)); got != step.want {
			t.Errorf("paso %d (tipo %d, +%s): allow = %v, quería %v", i, step.kind, step.at, got, step.want)
		}
	}
}

// TestSendProblem: el micrófono que entrega menos de captureMinRate o la PC que tira 100 ms o más
// avisan; una cola o un frame lento (solo atrasan) no.
func TestSendProblem(t *testing.T) {
	ok := captureInterval{elapsed: 10 * time.Second, frames: 500}
	for name, tc := range map[string]struct {
		c    captureInterval
		want problemKind
	}{
		"normal":             {ok, 0},
		"micrófono lento":    {captureInterval{elapsed: 10 * time.Second, frames: 470}, problemSlowMic},
		"tirados":            {captureInterval{elapsed: 10 * time.Second, frames: 500, dropped: captureWarnDropped}, problemBusyPC},
		"pocos tirados":      {captureInterval{elapsed: 10 * time.Second, frames: 500, dropped: captureWarnDropped - 1}, 0},
		"cola y frame lento": {captureInterval{elapsed: 10 * time.Second, frames: 500, maxQueue: 4, maxProc: 30 * time.Millisecond}, 0},
	} {
		if got := sendProblem(tc.c); got != tc.want {
			t.Errorf("%s: %d, quería %d", name, got, tc.want)
		}
	}
}

// TestSendWatch: un intervalo malo solo no avisa; dos seguidos del mismo problema sí, una vez por
// racha; uno sano o sin medir corta la racha.
func TestSendWatch(t *testing.T) {
	var w sendWatch
	seq := []struct {
		in, want problemKind
	}{
		{problemSlowMic, 0},
		{0, 0}, // sano: corta
		{problemSlowMic, 0},
		{problemSlowMic, problemSlowMic}, // dos seguidos
		{problemSlowMic, 0},              // la misma racha no repite
		{problemBusyPC, 0},               // otro problema: racha nueva
		{problemBusyPC, problemBusyPC},
		{0, 0},
		{problemSlowMic, 0},
		{problemSlowMic, problemSlowMic}, // racha nueva: avisa (el tope por tipo lo frena aparte)
	}
	for i, step := range seq {
		if got := w.observe(step.in); got != step.want {
			t.Errorf("paso %d: observe(%d) = %d, quería %d", i, step.in, got, step.want)
		}
	}
}

func activityTexts(a *App) []string {
	var texts []string
	for _, row := range a.activityBox.Objects {
		texts = append(texts, row.(*widget.RichText).String())
	}
	return texts
}

// TestReportProblemInActivityLog: un dispositivo que falta va al registro con la ayuda y a la barra
// sin ella; la segunda vez dentro del tope no se repite. Los problemas del envío no avisan con el
// micrófono muteado.
func TestReportProblemInActivityLog(t *testing.T) {
	test.NewTempApp(t).Settings().SetTheme(voxTheme{})
	a := New(filepath.Join(t.TempDir(), "voxmesh.exe"))
	setLanguage(t, "es") // después de New, que pone el de la config
	test.NewTempWindow(t, a.content())

	want := audio.Device{Name: "Auriculares USB", ID: "abc"}
	missing := audio.Resolved{Device: audio.Device{Name: audio.DefaultChoice.Name}, Missing: true}
	present := audio.Resolved{Device: audio.Device{Name: "Parlantes", ID: "def"}}
	notice := a.applyResolvedDevices(want, present.Device, missing, present)
	status := i18n.Join(notice, i18n.M("audio.ready", "x")).In(i18n.ES)
	if status != "No encontré el micrófono «Auriculares USB»: uso el predeterminado de Windows. Audio listo: x" {
		t.Errorf("barra de estado: %q", status)
	}
	rows := activityTexts(a)
	if len(rows) != 1 || !strings.HasSuffix(rows[0], "Si lo querés usar, conectalo y tocá Reiniciar audio, o elegí otro en Ajustes.") {
		t.Fatalf("registro: %q", rows)
	}
	a.applyResolvedDevices(want, present.Device, missing, present)
	if rows := activityTexts(a); len(rows) != 1 {
		t.Fatalf("dentro del tope se repitió: %q", rows)
	}
	if notice := a.applyResolvedDevices(present.Device, present.Device, present, present); notice.In(i18n.ES) != "" {
		t.Errorf("sin faltantes el aviso es %q", notice.In(i18n.ES))
	}

	slow := captureInterval{elapsed: 10 * time.Second, frames: 450}
	var w sendWatch
	a.isMuted.Store(true)
	a.watchSend(&w, slow, true)
	a.watchSend(&w, slow, true)
	if rows := activityTexts(a); len(rows) != 1 {
		t.Fatalf("muteado avisó: %q", rows)
	}
	a.isMuted.Store(false)
	a.watchSend(&w, slow, true)
	a.watchSend(&w, slow, true)
	rows = activityTexts(a)
	if len(rows) != 2 || !strings.Contains(rows[1], "Tu micrófono está perdiendo audio") {
		t.Fatalf("micrófono lento: %q", rows)
	}
}
