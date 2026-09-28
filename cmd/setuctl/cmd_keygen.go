package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
)

// runKeygen generates a signing key pair for development and testing.
//
// A production signing key is never generated this way. Spec 7 requires
// officer and issuance keys to live in an HSM and to be non-exportable, and a
// key ceremony to be documented. This command exists so that the publication
// and verification path can be exercised end to end in development, and it says
// so on the key it writes.
func runKeygen(args []string) error {
	fs := flagSet("keygen")
	out := fs.String("out", "ruleset-signing", "base path for the key files")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	privPath := *out + ".dev.key"
	pubPath := *out + ".dev.pub"

	if err := os.WriteFile(privPath, []byte(hex.EncodeToString(priv)+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, []byte(hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
		return err
	}

	fmt.Printf("wrote %s (private, mode 0600)\n", privPath)
	fmt.Printf("wrote %s (public)\n", pubPath)
	fmt.Println()
	fmt.Println("This is a DEVELOPMENT key. A ruleset served to applicants must be signed")
	fmt.Println("with an HSM-backed key under a documented key ceremony; a private key that")
	fmt.Println("can be read off a filesystem is not one.")
	return nil
}
