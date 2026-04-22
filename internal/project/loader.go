package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ivantit66/onebase/internal/dsl/ast"
	"github.com/ivantit66/onebase/internal/dsl/lexer"
	"github.com/ivantit66/onebase/internal/dsl/parser"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/report"
)

type Project struct {
	Dir       string
	Entities  []*metadata.Entity
	Registers []*metadata.Register
	Reports   []*report.Report
	Programs  map[string]*ast.Program // entity name → parsed DSL
}

func Load(dir string) (*Project, error) {
	p := &Project{
		Dir:      dir,
		Programs: make(map[string]*ast.Program),
	}
	if err := p.loadMetadata(); err != nil {
		return nil, err
	}
	if err := metadata.Validate(p.Entities); err != nil {
		return nil, err
	}
	if err := p.loadDSL(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Project) loadMetadata() error {
	type entry struct {
		subdir string
		kind   metadata.Kind
	}
	for _, e := range []entry{
		{"catalogs", metadata.KindCatalog},
		{"documents", metadata.KindDocument},
	} {
		dir := filepath.Join(p.Dir, e.subdir)
		items, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("readdir %s: %w", dir, err)
		}
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			ent, err := metadata.LoadFile(filepath.Join(dir, item.Name()), e.kind)
			if err != nil {
				return err
			}
			p.Entities = append(p.Entities, ent)
		}
	}
	// load registers
	regDir := filepath.Join(p.Dir, "registers")
	items, err := os.ReadDir(regDir)
	if err == nil {
		for _, item := range items {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			reg, err := metadata.LoadRegisterFile(filepath.Join(regDir, item.Name()))
			if err != nil {
				return err
			}
			p.Registers = append(p.Registers, reg)
		}
	}
	// load reports
	repDir := filepath.Join(p.Dir, "reports")
	repItems, err := os.ReadDir(repDir)
	if err == nil {
		for _, item := range repItems {
			if item.IsDir() || !strings.HasSuffix(item.Name(), ".yaml") {
				continue
			}
			rep, err := report.LoadFile(filepath.Join(repDir, item.Name()))
			if err != nil {
				return err
			}
			p.Reports = append(p.Reports, rep)
		}
	}
	return nil
}

func (p *Project) loadDSL() error {
	srcDir := filepath.Join(p.Dir, "src")
	items, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("readdir %s: %w", srcDir, err)
	}
	for _, item := range items {
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".os") {
			continue
		}
		fullPath := filepath.Join(srcDir, item.Name())
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return err
		}
		l := lexer.New(string(data), fullPath)
		pr := parser.New(l)
		prog, err := pr.ParseProgram()
		if err != nil {
			return err
		}
		entityName := fileNameToEntity(item.Name())
		p.Programs[entityName] = prog
	}
	return nil
}

// fileNameToEntity converts "invoice.os" → "Invoice", "счёт.os" → "Счёт".
func fileNameToEntity(name string) string {
	base := strings.TrimSuffix(name, ".os")
	if base == "" {
		return base
	}
	r, size := utf8.DecodeRuneInString(base)
	return string(unicode.ToUpper(r)) + base[size:]
}
