package chatgptplan

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// DPAPI binds stored credentials to the current Windows user. A copied file is
// insufficient to recover its tokens under another account.
func sealCredential(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("empty credential record")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(plain)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func unsealCredential(cipher []byte) ([]byte, error) {
	if len(cipher) == 0 {
		return nil, errors.New("empty credential record")
	}
	in := windows.DataBlob{Size: uint32(len(cipher)), Data: &cipher[0]}
	var out windows.DataBlob
	err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out)
	runtime.KeepAlive(cipher)
	if err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(out.Data))))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
