//go:build cgo

package ui

import (
	"errors"
	"strings"

	"voxmesh/internal/audio"
	"voxmesh/internal/config"
	"voxmesh/internal/i18n"
	"voxmesh/internal/invite"
	"voxmesh/internal/phonemic"
)

// userErrors son los errores de otros paquetes que son una equivocación del usuario (una
// invitación mal copiada, un nombre de perfil, un archivo de sonido que no sirve), no una falla
// del sistema: se muestran en el idioma de la app. El texto en español de cada clave es el del
// error seguido de %s (TestUserErrorsKeepSpanishText).
var userErrors = []struct {
	err error
	key string
}{
	{invite.ErrNotInvite, "err.invite.not_invite"},
	{invite.ErrVersion, "err.invite.version"},
	{invite.ErrEncoding, "err.invite.encoding"},
	{invite.ErrAddress, "err.invite.address"},
	{invite.ErrKey, "err.invite.key"},
	{config.ErrProfileNameEmpty, "err.profile.empty"},
	{config.ErrProfileNameDefault, "err.profile.default"},
	{config.ErrProfileNameTooLong, "err.profile.too_long"},
	{config.ErrProfileExists, "err.profile.exists"},
	{config.ErrDefaultProfileFixed, "err.profile.default_fixed"},
	{audio.ErrSoundFormat, "err.sound.format"},
	{audio.ErrSoundMP3Invalid, "err.sound.mp3_invalid"},
	{audio.ErrSoundWAVInvalid, "err.sound.wav_invalid"},
	{audio.ErrSoundWAVFmt, "err.sound.wav_fmt"},
	{audio.ErrSoundWAVNoData, "err.sound.wav_no_data"},
	{audio.ErrSoundWAVBits, "err.sound.wav_bits"},
	{audio.ErrSoundEmpty, "err.sound.empty"},
	{audio.ErrSoundTooShort, "err.sound.too_short"},
	{phonemic.ErrNoLocalIP, "err.phone.no_local_ip"},
}

// errorMsg es el texto de err para el usuario. Un error de userErrors sale del catálogo, con lo
// que se le agregó detrás (" (31 bytes)", el nombre del perfil); cualquier otro (dispositivos, red,
// disco, Windows) va tal cual, como detalle técnico de un mensaje ya traducido.
func errorMsg(err error) i18n.Msg {
	text := err.Error()
	for _, known := range userErrors {
		if !errors.Is(err, known.err) {
			continue
		}
		if rest, ok := strings.CutPrefix(text, known.err.Error()); ok {
			return i18n.M(known.key, i18n.Plain(rest))
		}
	}
	return i18n.Plain(text)
}
