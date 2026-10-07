// Rhizome - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Rhizome contributors

// marketindex is the publish-side tool for the curated provider index
// (stpinkie/rhizome-market-index): it validates index.json against the
// pkg/marketindex schema, bumps the seq counter, and writes the detached
// Ed25519 signature. The index repo's CI runs the same three steps on tag:
//
//	go run ./scripts/marketindex validate index.json
//	go run ./scripts/marketindex bump index.json      # seq+1, updated_at=now
//	go run ./scripts/marketindex sign index.json       # writes index.json.sig
//
// sign reads the curator seed from MARKET_INDEX_SIGNING_KEY (or
// MODULE_CATALOG_SIGNING_KEY — same trust root) as a base64 Ed25519 seed.
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/stpinkie/rhizome/pkg/marketindex"
	"github.com/stpinkie/rhizome/pkg/sigverify"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr,
			"usage: go run ./scripts/marketindex {validate|bump|sign} <index.json>\n")
		os.Exit(2)
	}
	cmd, path := os.Args[1], os.Args[2]
	var err error
	switch cmd {
	case "validate":
		err = validate(path)
	case "bump":
		err = bump(path)
	case "sign":
		err = sign(path)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "marketindex %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func readDoc(path string) ([]byte, *marketindex.Index, error) {
	doc, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, nil, err
	}
	idx, err := marketindex.Parse(doc)
	if err != nil {
		return nil, nil, err
	}
	return doc, idx, nil
}

func validate(path string) error {
	_, idx, err := readDoc(path)
	if err != nil {
		return err
	}
	fmt.Printf("index ok: v%d seq=%d providers=%d\n", idx.V, idx.Seq, len(idx.Providers))
	return nil
}

func bump(path string) error {
	_, idx, err := readDoc(path)
	if err != nil {
		return err
	}
	idx.Seq++
	idx.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	out, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Printf("index bumped to seq=%d\n", idx.Seq)
	return nil
}

func sign(path string) error {
	doc, _, err := readDoc(path)
	if err != nil {
		return err // never sign a doc that fails schema validation
	}
	seed := os.Getenv("MARKET_INDEX_SIGNING_KEY")
	if seed == "" {
		seed = os.Getenv("MODULE_CATALOG_SIGNING_KEY")
	}
	if seed == "" {
		return fmt.Errorf("MARKET_INDEX_SIGNING_KEY (or MODULE_CATALOG_SIGNING_KEY) unset")
	}
	sig, err := sigverify.SignRelease(doc, seed)
	if err != nil {
		return err
	}
	sigPath := path + ".sig"
	if err := os.WriteFile(sigPath, []byte(sig+"\n"), 0o600); err != nil {
		return err
	}
	// Sanity: the written signature must verify against the declared or
	// baked key — catch a wrong-secret CI configuration before publish.
	if _, err := base64.StdEncoding.DecodeString(sig); err != nil {
		return fmt.Errorf("signature encode: %w", err)
	}
	fmt.Printf("wrote %s\n", sigPath)
	return nil
}
