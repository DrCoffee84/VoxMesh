//go:build windows && cgo

package ui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/image/bmp"
)

var (
	comdlg32         = syscall.NewLazyDLL("comdlg32.dll")
	getOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")
	commDlgError     = comdlg32.NewProc("CommDlgExtendedError")

	clipUser32                 = syscall.NewLazyDLL("user32.dll")
	openClipboardProc          = clipUser32.NewProc("OpenClipboard")
	closeClipboardProc         = clipUser32.NewProc("CloseClipboard")
	getClipboardDataProc       = clipUser32.NewProc("GetClipboardData")
	isClipboardFormatAvailable = clipUser32.NewProc("IsClipboardFormatAvailable")
	registerClipboardFormatW   = clipUser32.NewProc("RegisterClipboardFormatW")

	clipKernel32 = syscall.NewLazyDLL("kernel32.dll")
	globalLock   = clipKernel32.NewProc("GlobalLock")
	globalUnlock = clipKernel32.NewProc("GlobalUnlock")
	globalSize   = clipKernel32.NewProc("GlobalSize")

	clipShell32    = syscall.NewLazyDLL("shell32.dll")
	dragQueryFileW = clipShell32.NewProc("DragQueryFileW")
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

func readClipboardImage() (string, []byte, string, error) {
	ret, _, _ := openClipboardProc.Call(0)
	if ret == 0 {
		return "", nil, "", errors.New("no se pudo abrir el portapapeles")
	}
	defer closeClipboardProc.Call()

	// 1. Formato registrado PNG (navegadores modernos, electron, apps)
	pngFormatName, _ := syscall.UTF16PtrFromString("PNG")
	pngFormat, _, _ := registerClipboardFormatW.Call(uintptr(unsafe.Pointer(pngFormatName)))
	if pngFormat != 0 {
		avail, _, _ := isClipboardFormatAvailable.Call(pngFormat)
		if avail != 0 {
			hMem, _, _ := getClipboardDataProc.Call(pngFormat)
			if hMem != 0 {
				ptr, _, _ := globalLock.Call(hMem)
				if ptr != 0 {
					size, _, _ := globalSize.Call(hMem)
					if size > 0 {
						data := make([]byte, size)
						copy(data, unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size))
						globalUnlock.Call(hMem)
						return ".png", data, "portapapeles.png", nil
					}
					globalUnlock.Call(hMem)
				}
			}
		}
	}

	// 2. Archivos copiados desde el explorador de Windows (CF_HDROP)
	const cfHDrop = 15
	avail, _, _ := isClipboardFormatAvailable.Call(cfHDrop)
	if avail != 0 {
		hDrop, _, _ := getClipboardDataProc.Call(cfHDrop)
		if hDrop != 0 {
			count, _, _ := dragQueryFileW.Call(hDrop, 0xFFFFFFFF, 0, 0)
			if count > 0 {
				buf := make([]uint16, 1024)
				fLen, _, _ := dragQueryFileW.Call(hDrop, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
				if fLen > 0 {
					filePath := syscall.UTF16ToString(buf[:fLen])
					ext := strings.ToLower(filepath.Ext(filePath))
					if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".webp" {
						data, err := os.ReadFile(filePath)
						if err == nil && len(data) > 0 {
							return ext, data, filepath.Base(filePath), nil
						}
					}
				}
			}
		}
	}

	// 3. Captura nativa DIB de Windows (CF_DIB)
	const cfDIB = 8
	avail, _, _ = isClipboardFormatAvailable.Call(cfDIB)
	if avail != 0 {
		hMem, _, _ := getClipboardDataProc.Call(cfDIB)
		if hMem != 0 {
			ptr, _, _ := globalLock.Call(hMem)
			if ptr != 0 {
				size, _, _ := globalSize.Call(hMem)
				if size > 40 {
					dibData := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), size)
					pngData, err := dibToPNG(dibData)
					globalUnlock.Call(hMem)
					if err == nil && len(pngData) > 0 {
						return ".png", pngData, "captura.png", nil
					}
				} else {
					globalUnlock.Call(hMem)
				}
			}
		}
	}

	return "", nil, "", errors.New("no hay imagen en el portapapeles")
}

func dibToPNG(dibData []byte) ([]byte, error) {
	if len(dibData) < 40 {
		return nil, errors.New("DIB demasiado pequeño")
	}
	headerSize := binary.LittleEndian.Uint32(dibData[0:4])
	bitCount := binary.LittleEndian.Uint16(dibData[14:16])
	compression := binary.LittleEndian.Uint32(dibData[16:20])
	clrUsed := binary.LittleEndian.Uint32(dibData[32:36])

	var paletteSize uint32 = 0
	if clrUsed > 0 {
		paletteSize = clrUsed * 4
	} else if bitCount <= 8 {
		paletteSize = (1 << bitCount) * 4
	} else if compression == 3 {
		paletteSize = 12
	}

	offBits := 14 + headerSize + paletteSize
	fileSize := 14 + uint32(len(dibData))

	bmpBytes := make([]byte, fileSize)
	bmpBytes[0] = 'B'
	bmpBytes[1] = 'M'
	binary.LittleEndian.PutUint32(bmpBytes[2:6], fileSize)
	binary.LittleEndian.PutUint32(bmpBytes[6:10], 0)
	binary.LittleEndian.PutUint32(bmpBytes[10:14], offBits)
	copy(bmpBytes[14:], dibData)

	img, err := bmp.Decode(bytes.NewReader(bmpBytes))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
