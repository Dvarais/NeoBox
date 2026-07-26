package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
)

func main() {
	keyFlag := flag.String("key", "", "Hex-encoded Ed25519 private key (64 bytes/128 hex characters)")
	fileFlag := flag.String("file", "", "Path to the binary file to sign")
	outFlag := flag.String("out", "", "Path to save the hex signature (optional, defaults to <file>.sig)")
	flag.Parse()

	if *keyFlag == "" || *fileFlag == "" {
		fmt.Println("Usage: go run cmd/sign/main.go -key <private_key_hex> -file <path_to_binary> [-out <signature_file>]")
		os.Exit(1)
	}

	// Read private key
	privKeyBytes, err := hex.DecodeString(*keyFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding private key: %v\n", err)
		os.Exit(1)
	}
	if len(privKeyBytes) != ed25519.PrivateKeySize {
		fmt.Fprintf(os.Stderr, "Invalid private key size: got %d bytes, expected %d\n", len(privKeyBytes), ed25519.PrivateKeySize)
		os.Exit(1)
	}
	privKey := ed25519.PrivateKey(privKeyBytes)

	// Read file
	fileData, err := os.ReadFile(*fileFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
		os.Exit(1)
	}

	// Compute SHA-256 hash of the file
	hash := sha256.Sum256(fileData)

	// Sign the hash
	sigBytes := ed25519.Sign(privKey, hash[:])

	// Hex encode signature
	sigHex := hex.EncodeToString(sigBytes)

	// Determine output path
	outPath := *outFlag
	if outPath == "" {
		outPath = *fileFlag + ".sig"
	}

	// Save or print signature
	err = os.WriteFile(outPath, []byte(sigHex), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing signature file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully signed %s\n", *fileFlag)
	fmt.Printf("Signature (hex): %s\n", sigHex)
	fmt.Printf("Saved to: %s\n", outPath)
}
