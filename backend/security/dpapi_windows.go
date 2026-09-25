//go:build windows

package security

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// dpapiProtect encrypts data using Windows DPAPI (CryptProtectData).
// Only the same user account on the same machine can decrypt it.
func dpapiProtect(data []byte) ([]byte, error) {
	var dataIn windows.DataBlob
	dataIn.Size = uint32(len(data))
	dataIn.Data = &data[0]

	var dataOut windows.DataBlob
	defer func() {
		if dataOut.Data != nil {
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(dataOut.Data)))
		}
	}()

	err := windows.CryptProtectData(&dataIn, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dataOut)
	if err != nil {
		return nil, fmt.Errorf("CryptProtectData failed: %w", err)
	}

	protected := make([]byte, dataOut.Size)
	copy(protected, unsafe.Slice(dataOut.Data, dataOut.Size))
	return protected, nil
}

// dpapiUnprotect decrypts data using Windows DPAPI (CryptUnprotectData).
func dpapiUnprotect(data []byte) ([]byte, error) {
	var dataIn windows.DataBlob
	dataIn.Size = uint32(len(data))
	dataIn.Data = &data[0]

	var dataOut windows.DataBlob
	defer func() {
		if dataOut.Data != nil {
			_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(dataOut.Data)))
		}
	}()

	err := windows.CryptUnprotectData(&dataIn, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &dataOut)
	if err != nil {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}

	unprotected := make([]byte, dataOut.Size)
	copy(unprotected, unsafe.Slice(dataOut.Data, dataOut.Size))
	return unprotected, nil
}

// LockMemory pins data in RAM on Windows to prevent swapping to disk.
func LockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	addr := uintptr(unsafe.Pointer(&data[0]))
	size := uintptr(len(data))
	if err := windows.VirtualLock(addr, size); err != nil {
		return fmt.Errorf("VirtualLock failed: %w", err)
	}
	return nil
}

// UnlockMemory releases a region pinned by LockMemory.
func UnlockMemory(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	addr := uintptr(unsafe.Pointer(&data[0]))
	size := uintptr(len(data))
	if err := windows.VirtualUnlock(addr, size); err != nil {
		return fmt.Errorf("VirtualUnlock failed: %w", err)
	}
	return nil
}
