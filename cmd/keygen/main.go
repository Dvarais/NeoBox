package main

import (
	"NeoBox/backend/security"
	"fmt"
)

func main() {
	pub, priv, err := security.GenerateSigningKeys()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Public:", pub)   // Вставить в signature.go в константу PublicKeyHex
	fmt.Println("Private:", priv) // Хранить офлайн! Используется для подписи релизов.
}
