// This release fixture implements the future command's process boundary only.
// It is not the vibe-init implementation or an end-to-end client registration.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	mode := os.Getenv("BOOTSTRAP_FIXTURE_MODE")
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		if mode == "version-fail" {
			os.Exit(21)
		}
		if winner := os.Getenv("BOOTSTRAP_FIXTURE_WINNER"); winner != "" {
			if err := os.MkdirAll(filepath.Dir(winner), 0700); err != nil {
				panic(err)
			}
			if err := os.WriteFile(winner, []byte("concurrent winner"), 0600); err != nil {
				panic(err)
			}
		}
		fmt.Println("onebase release-fixture")
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "vibe-init" {
		if mode == "old" {
			os.Exit(22)
		}
		if os.Args[2] == "--help" {
			fmt.Println("vibe-init DIR [--agents LIST]")
			return
		}
		if mode == "command-fail" {
			os.Exit(23)
		}
		data, err := json.Marshal(os.Args[1:])
		if err != nil {
			panic(err)
		}
		if err := os.WriteFile(os.Getenv("BOOTSTRAP_FIXTURE_ARGS"), data, 0600); err != nil {
			panic(err)
		}
		return
	}
	os.Exit(24)
}
