package security

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	dllcrypt32            = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = dllcrypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = dllcrypt32.NewProc("CryptUnprotectData")
)

type DATA_BLOB struct {
	cbData uint32
	pbData *byte
}

// Encrypt encrypts a byte slice using the Windows DPAPI (CryptProtectData).
func Encrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("data to encrypt is empty")
	}

	var inBlob DATA_BLOB
	inBlob.cbData = uint32(len(data))
	inBlob.pbData = &data[0]

	var outBlob DATA_BLOB
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %w", err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(outBlob.pbData)))

	result := make([]byte, outBlob.cbData)
	copy(result, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return result, nil
}

// Decrypt decrypts a byte slice using the Windows DPAPI (CryptUnprotectData).
func Decrypt(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("encrypted data is empty")
	}

	var inBlob DATA_BLOB
	inBlob.cbData = uint32(len(data))
	inBlob.pbData = &data[0]

	var outBlob DATA_BLOB
	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&inBlob)),
		0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&outBlob)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", err)
	}
	defer syscall.LocalFree(syscall.Handle(unsafe.Pointer(outBlob.pbData)))

	result := make([]byte, outBlob.cbData)
	copy(result, unsafe.Slice(outBlob.pbData, outBlob.cbData))
	return result, nil
}
