package winproc

// AutostartFlag va en el comando que Windows ejecuta al iniciar sesión (AGENDA F12): con él la
// app arranca escondida junto al reloj.
const AutostartFlag = "--autostart"

// LaunchedAtLogin dice si la abrió Windows al iniciar sesión (los argumentos sin el exe).
func LaunchedAtLogin(args []string) bool {
	for _, arg := range args {
		if arg == AutostartFlag {
			return true
		}
	}
	return false
}
