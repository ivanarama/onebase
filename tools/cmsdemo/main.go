// cmsdemo prepares a disposable, self-contained SQLite demo for release archives.
// Run from the repository root; the output must not already exist.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ivantit66/onebase/internal/auth"
	"github.com/ivantit66/onebase/internal/configdb"
	"github.com/ivantit66/onebase/internal/storage"
)

func main() {
	if err := build(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build() error {
	binary := flag.String("binary", "./onebase", "host-native onebase executable")
	project := flag.String("project", "examples/cms", "CMS project directory")
	output := flag.String("out", "dist-demo/cms-orders.db", "new SQLite database")
	flag.Parse()
	executable, err := filepath.Abs(*binary)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("create new demo: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	run := func(input string, args ...string) error {
		cmd := exec.Command(executable, append([]string{"--no-gui"}, args...)...) //nolint:gosec // G204: explicit host binary selected by the release builder; no shell or network input.
		cmd.Stdin = strings.NewReader(input)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	base := []string{"--project", *project, "--sqlite", *output}
	command := func(input string, args ...string) error { return run(input, append(args, base...)...) }
	if err := command("", "migrate"); err != nil {
		return err
	}
	// A release example must survive moving just its database to another machine.
	// The platform default is disk storage, which would leave media bytes behind.
	demoDB, err := storage.ConnectSQLite(context.Background(), *output)
	if err != nil {
		return err
	}
	if err := demoDB.SaveFileStorageMode(context.Background(), storage.FileStorageDB); err != nil {
		demoDB.Close()
		return err
	}
	demoDB.Close()
	for _, proc := range []string{"ЗаполнитьТестовуюБазу", "ПодготовитьДемоЗаказов"} {
		if err := command("", "procrun", "--proc", proc); err != nil {
			return err
		}
	}
	// These passwords are public sample credentials, never production secrets.
	if err := command("DemoLocal2026!\n", "user", "add", "admin", "--name", "Администратор демо", "--admin", "--password-stdin"); err != nil {
		return err
	}
	if err := command("DemoOrders2026!\n", "user", "add", "manager", "--name", "Менеджер демонстрации", "--show-in-list", "--password-stdin"); err != nil {
		return err
	}
	if err := command("", "user", "role", "assign", "manager", "МенеджерЗаказов"); err != nil {
		return err
	}
	if err := run("", "settings", "set", "net.enabled", "вкл", "--sqlite", *output); err != nil {
		return err
	}
	ctx := context.Background()
	db, err := storage.ConnectSQLite(ctx, *output)
	if err != nil {
		return err
	}
	defer db.Close()
	users := auth.NewRepo(db)
	list, err := users.List(ctx)
	if err != nil {
		return err
	}
	for _, user := range list {
		if user.Login == "manager" {
			if err := users.SetDenyPasswdChange(ctx, user.ID, true); err != nil {
				return err
			}
		}
	}
	repo := configdb.New(db)
	if err := repo.EnsureSchema(ctx); err != nil {
		return err
	}
	if err := repo.ImportFromDir(ctx, *project); err != nil {
		return err
	}
	if _, err := repo.CreateVersion(ctx, configdb.VersionOptions{Message: "Готовый пример: сайт и заказы"}); err != nil {
		return err
	}
	fmt.Println("Prepared", *output, "— add as an existing SQLite database in the launcher")
	return nil
}
