//go:build windows && cgo

package ui

import (
	"errors"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	getOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	commDlgError     = comdlg32.NewProc("CommDlgExtendedError")
)

type openFileName struct {
	structSize       uint32
	hwndOwner        uintptr
	hInstance        uintptr
	filter           *uint16
	customFilter     *uint16
	maxCustomFilter  uint32
	filterIndex      uint32
	file             *uint16
	maxFile          uint32
	fileTitle        *uint16
	maxFileTitle     uint32
	initialDirectory *uint16
	title            *uint16
	flags            uint32
	fileOffset       uint16
	fileExtension    uint16
	defaultExtension *uint16
	custData         uintptr
	hook             uintptr
	templateName     *uint16
	reserved         uintptr
	reserved2        uint32
	flagsEx          uint32
}

func selectImageFile() (string, error) {
	buffer := make([]uint16, 32768)
	filter := utf16.Encode([]rune("Imágenes (*.png;*.jpg;*.jpeg;*.gif;*.webp\x00*.png;*.jpg;*.jpeg;*.gif;*.webp\x00Todos los archivos\x00*.*\x00"))
	filter = append(filter, 0)
	title, _ := syscall.UTF16PtrFromString("Seleccionar imagen")
	dialog := openFileName{
		structSize: uint32(unsafe.Sizeof(openFileName{})),
		filter:     &filter[0],
		file:       &buffer[0],
		maxFile:    uint32(len(buffer)),
		title:      title,
		flags:      0x00001000 | 0x00000800,
	}
	result, _, callErr := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		if code, _, _ := commDlgError.Call(); code != 0 {
			return "", callErr
		}
		return "", errors.New("selección cancelada")
	}
	return syscall.UTF16ToString(buffer), nil
}

func selectSoundFile() (string, error) {
	buffer := make([]uint16, 32768)
	filter := utf16.Encode([]rune("Sonidos (*.wav;*.mp3)\x00*.wav;*.mp3\x00Todos los archivos\x00*.*\x00"))
	filter = append(filter, 0)
	title, _ := syscall.UTF16PtrFromString("Seleccionar sonido")
	dialog := openFileName{
		structSize: uint32(unsafe.Sizeof(openFileName{})),
		filter:     &filter[0],
		file:       &buffer[0],
		maxFile:    uint32(len(buffer)),
		title:      title,
		flags:      0x00001000 | 0x00000800,
	}
	result, _, callErr := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		if code, _, _ := commDlgError.Call(); code != 0 {
			return "", callErr
		}
		return "", errors.New("selección cancelada")
	}
	return syscall.UTF16ToString(buffer), nil
}
