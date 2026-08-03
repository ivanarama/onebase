package gengen

import "testing"

func TestComputeDelta_NewCatalog(t *testing.T) {
	requested := &ResolvedManifest{
		Catalogs: []EntitySpec{
			{Name: "Контрагент", Fields: []FieldSpec{{Name: "Наименование", Type: "string"}}},
		},
	}
	existing := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.NewCatalogs) != 1 {
		t.Fatalf("expected 1 new catalog, got %d", len(delta.NewCatalogs))
	}
	if delta.NewCatalogs[0].Name != "Контрагент" {
		t.Errorf("expected Контрагент, got %s", delta.NewCatalogs[0].Name)
	}
}

func TestComputeDelta_ExistingCatalog_NewFields(t *testing.T) {
	requested := &ResolvedManifest{
		Catalogs: []EntitySpec{
			{
				Name: "Контрагент",
				Fields: []FieldSpec{
					{Name: "Наименование", Type: "string"},
					{Name: "ИНН", Type: "string"},
					{Name: "КПП", Type: "string"},
				},
			},
		},
	}
	existing := &ExistingManifest{
		Catalogs: map[string]CatalogInfo{
			"Контрагент": {
				Name: "Контрагент",
				Fields: []FieldInfo{
					{Name: "Наименование", Type: "string"},
					{Name: "ИНН", Type: "string"},
				},
			},
		},
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.NewCatalogs) != 0 {
		t.Errorf("expected 0 new catalogs, got %d", len(delta.NewCatalogs))
	}

	newFields, ok := delta.NewFields["Контрагент"]
	if !ok {
		t.Fatal("expected new fields for Контрагент")
	}
	if len(newFields) != 1 {
		t.Fatalf("expected 1 new field, got %d", len(newFields))
	}
	if newFields[0].Name != "КПП" {
		t.Errorf("expected КПП, got %s", newFields[0].Name)
	}
}

func TestComputeDelta_NewDocument(t *testing.T) {
	requested := &ResolvedManifest{
		Documents: []EntitySpec{
			{Name: "РеализацияТоваров", Fields: []FieldSpec{{Name: "Дата", Type: "date"}}},
		},
	}
	existing := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.NewDocuments) != 1 {
		t.Fatalf("expected 1 new document, got %d", len(delta.NewDocuments))
	}
}

func TestComputeDelta_NewEnum(t *testing.T) {
	requested := &ResolvedManifest{
		Enums: []EnumSpec{
			{Name: "СтавкиНДС", Values: []string{"БезНДС", "20%"}},
		},
	}
	existing := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.NewEnums) != 1 {
		t.Fatalf("expected 1 new enum, got %d", len(delta.NewEnums))
	}
}

func TestComputeDelta_NewDSLFile(t *testing.T) {
	requested := &ResolvedManifest{
		DSLFiles: map[string]string{
			"отчёт_продажи.os": "Процедура Сформировать()\nКонецПроцедуры\n",
		},
	}
	existing := &ExistingManifest{
		Catalogs:  make(map[string]CatalogInfo),
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.NewDSLFiles) != 1 {
		t.Fatalf("expected 1 new DSL file, got %d", len(delta.NewDSLFiles))
	}
}

func TestComputeDelta_NoChanges(t *testing.T) {
	requested := &ResolvedManifest{
		Catalogs: []EntitySpec{
			{Name: "Контрагент", Fields: []FieldSpec{{Name: "Наименование", Type: "string"}}},
		},
	}
	existing := &ExistingManifest{
		Catalogs: map[string]CatalogInfo{
			"Контрагент": {
				Name:   "Контрагент",
				Fields: []FieldInfo{{Name: "Наименование", Type: "string"}},
			},
		},
		Documents: make(map[string]DocumentInfo),
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if delta.HasChanges() {
		t.Error("expected no changes")
	}
}

func TestComputeDelta_Conflict(t *testing.T) {
	requested := &ResolvedManifest{
		Catalogs: []EntitySpec{
			{Name: "РеализацияТоваров", Fields: []FieldSpec{{Name: "Дата", Type: "date"}}},
		},
	}
	existing := &ExistingManifest{
		Catalogs: make(map[string]CatalogInfo),
		Documents: map[string]DocumentInfo{
			"РеализацияТоваров": {Name: "РеализацияТоваров"},
		},
		Registers: make(map[string]RegisterInfo),
		Enums:     make(map[string]EnumInfo),
		DSLFiles:  make(map[string]string),
	}

	delta := ComputeDelta(requested, existing)

	if len(delta.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(delta.Conflicts))
	}
	if delta.Conflicts[0].Name != "РеализацияТоваров" {
		t.Errorf("expected conflict name = РеализацияТоваров, got %s", delta.Conflicts[0].Name)
	}
}

func TestDeltaManifest_Summary(t *testing.T) {
	delta := &DeltaManifest{
		NewCatalogs:  []EntitySpec{{Name: "Контрагент"}},
		NewDocuments: []EntitySpec{{Name: "РеализацияТоваров"}},
		NewFields: map[string][]FieldSpec{
			"Контрагент": {{Name: "КПП", Type: "string"}},
		},
	}

	summary := delta.Summary()
	if summary == "" {
		t.Fatal("expected non-empty summary")
	}
}

func TestDeltaManifest_Summary_NoChanges(t *testing.T) {
	delta := &DeltaManifest{
		NewFields:     make(map[string][]FieldSpec),
		NewTableParts: make(map[string][]TablePartSpec),
		NewDSLFiles:   make(map[string]string),
	}

	summary := delta.Summary()
	if summary != "Нет изменений — всё уже существует" {
		t.Errorf("unexpected summary: %s", summary)
	}
}
