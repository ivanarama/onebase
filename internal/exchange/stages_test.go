package exchange_test

// Этапы (план 121) на пути обмена данными (план 86) — четвёртый независимый
// путь записи объекта. Объект приезжает из базы-источника, где маршрут он уже
// прошёл по её правилам и её конфигурации: гейт переходов здесь обязан
// пропустить любое состояние, иначе расхождение блоков `stages` между узлами
// рвало бы обмен. Но история фиксирует, что переход внешний.

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

func stagesCatalog() *metadata.Entity {
	return &metadata.Entity{
		Name: "Заявка", Kind: metadata.KindCatalog,
		Fields: []metadata.Field{
			{Name: "Наименование", Type: metadata.FieldTypeString},
			{Name: "Состояние", Type: "enum:СостояниеЗаявки", EnumName: "СостояниеЗаявки"},
		},
		Stages: &metadata.Stages{
			Field: "Состояние",
			Order: []string{"Черновик", "НаСогласовании", "Утверждена"},
			Transitions: []metadata.StageTransition{
				{From: "Черновик", To: []string{"НаСогласовании"}},
				{From: "НаСогласовании", To: []string{"Утверждена"}},
			},
			Enforce: metadata.StageEnforceStrict,
		},
	}
}

func stagesPlan() *metadata.ExchangePlan {
	p := &metadata.ExchangePlan{
		Name: "Обмен", Content: []string{"Справочник.Заявка"},
		Nodes: []metadata.ExchangeNode{{Code: "center"}, {Code: "fil01"}},
	}
	p.Normalize()
	return p
}

func TestStagesExchangeAppliesForeignStageAndMarksSource(t *testing.T) {
	ent := stagesCatalog()
	res := fakeResolver{"Заявка": ent}
	a, ctxA := newBase(t, ent)
	b, ctxB := newBase(t, ent)
	_ = a.SaveExchangeThisNode(ctxA, "Обмен", "center")
	_ = b.SaveExchangeThisNode(ctxB, "Обмен", "fil01")

	// В базе-источнике объект уже на «Утверждена»: там он прошёл маршрут
	// целиком. Пишем его мимо гейта тем же признаком внешней записи, каким
	// пользуется приёмка, — иначе не собрать исходное состояние.
	id := uuid.New()
	if err := a.ApplyReplicatedEntity(ctxA, ent.Name, id, map[string]any{
		"Наименование": "Заявка из центра", "Состояние": "Утверждена",
	}, ent, `["exchange","Обмен","seed",0]`); err != nil {
		t.Fatal(err)
	}
	v, _ := a.EntityVersion(ctxA, ent.Name, id)
	if err := a.RegisterExchangeChange(ctxA, storage.ExchangeChange{
		Plan: "Обмен", ObjectType: ent.Name, ObjectID: id.String(),
		NodeCode: "fil01", Version: v, ChangedAt: 1000,
	}); err != nil {
		t.Fatal(err)
	}

	data, err := exchange.BuildPackage(ctxA, a, res, stagesPlan(), "fil01")
	if err != nil {
		t.Fatalf("BuildPackage: %v", err)
	}

	// На приёмнике объекта нет: по правилам маршрута создать его сразу на
	// «Утверждена» нельзя, и обычная запись была бы отвергнута. Приёмка обязана
	// пройти.
	lr, err := exchange.ApplyPackage(ctxB, b, res, stagesPlan(), data, exchange.ApplyOptions{})
	if err != nil {
		t.Fatalf("ApplyPackage: %v — гейт этапов не должен рвать обмен", err)
	}
	if lr.Applied != 1 {
		t.Fatalf("загружено %+v, ожидался 1 объект", lr)
	}

	row, err := b.GetByID(ctxB, ent.Name, id, ent)
	if err != nil {
		t.Fatalf("объект не загружен: %v", err)
	}
	if row["Состояние"] != "Утверждена" {
		t.Fatalf("этап на приёмнике = %v, ожидалась «Утверждена»", row["Состояние"])
	}

	hist, err := b.StageHistory(ctxB, ent.Name, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 {
		t.Fatalf("история на приёмнике: %d записей, ожидалась 1: %+v", len(hist), hist)
	}
	if hist[0].Source != storage.StageSourceExchange {
		t.Fatalf("источник записи %q, ожидался %q — иначе непонятно, откуда взялся переход",
			hist[0].Source, storage.StageSourceExchange)
	}
	if hist[0].ToStage != "Утверждена" {
		t.Fatalf("записан переход в %q", hist[0].ToStage)
	}
	// Происхождение — структурой, по которой видно, из какого плана, узла и
	// сообщения приехал переход.
	var ref []any
	if err := json.Unmarshal([]byte(hist[0].SourceRef), &ref); err != nil {
		t.Fatalf("происхождение %q не разбирается: %v", hist[0].SourceRef, err)
	}
	if len(ref) != 4 || ref[0] != "exchange" || ref[1] != "Обмен" || ref[2] != "center" {
		t.Fatalf("происхождение %v", ref)
	}
}
