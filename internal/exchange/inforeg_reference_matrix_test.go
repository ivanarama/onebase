package exchange_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ivantit66/onebase/internal/dbtest"
	"github.com/ivantit66/onebase/internal/exchange"
	"github.com/ivantit66/onebase/internal/metadata"
	"github.com/ivantit66/onebase/internal/storage"
)

// Получатель уже перевёл ресурс из строки в ссылку, а источник ещё работает
// по прежней схеме. Обмен принимает пакет, обычная запись остаётся строгой.
func TestApplyPackageInfoRegReferenceSchemaMismatchMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, recvDB *storage.DB) {
		ctx := context.Background()
		sendDB, sendCtx, sender := newInfoRegBase(t)
		receiver := infoRegCodes()
		receiver.Resources[0].Type = "reference:Товар"
		receiver.Resources[0].RefEntity = "Товар"
		if err := recvDB.MigrateInfoRegisters(ctx, []*metadata.InfoRegister{receiver}); err != nil {
			t.Fatal(err)
		}
		plan := infoRegPlan()
		if err := sendDB.SaveExchangeThisNode(sendCtx, plan.Name, "center"); err != nil {
			t.Fatal(err)
		}
		if err := recvDB.SaveExchangeThisNode(ctx, plan.Name, "fil01"); err != nil {
			t.Fatal(err)
		}

		validID := uuid.New().String()
		cases := []struct {
			key, value string
			want       any
		}{
			{"presentation", "ПОС-00001", nil},
			{"uuid", validID, validID},
			{"empty", "", nil},
		}
		for _, tc := range cases {
			dims := map[string]any{"Ключ": tc.key}
			if err := sendDB.InfoRegSet(sendCtx, sender, dims, map[string]any{"Значение": tc.value}, nil); err != nil {
				t.Fatal(err)
			}
			if err := exchange.RegisterInfoRegOnSave(sendCtx, sendDB, []*metadata.ExchangePlan{plan}, sender, dims, false); err != nil {
				t.Fatal(err)
			}
		}
		sendResolver := fakeResolverIR{inforegs: map[string]*metadata.InfoRegister{sender.Name: sender}}
		recvResolver := fakeResolverIR{inforegs: map[string]*metadata.InfoRegister{receiver.Name: receiver}}
		data, err := exchange.BuildPackage(sendCtx, sendDB, sendResolver, plan, "fil01")
		if err != nil {
			t.Fatal(err)
		}
		result, err := exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if err != nil {
			t.Fatalf("пакет с ресурсом прежнего типа отклонён: %v", err)
		}
		if result.Applied != len(cases) {
			t.Fatalf("применение пакета: %+v", result)
		}
		for _, tc := range cases {
			rec, err := recvDB.InfoRegGet(ctx, receiver, map[string]any{"Ключ": tc.key})
			if err != nil {
				t.Fatal(err)
			}
			if got := rec["Значение"]; got != tc.want {
				t.Errorf("%s: значение = %#v, ожидалось %#v", tc.key, got, tc.want)
			}
		}

		// Обменный режим не должен оставаться в контексте вызывающего кода.
		dims := map[string]any{"Ключ": "uuid"}
		err = recvDB.InfoRegSet(ctx, receiver, dims, map[string]any{"Значение": "ПОС-00001"}, nil)
		if !errors.Is(err, storage.ErrReferenceTypeMismatch) {
			t.Fatalf("локальная запись представления: %v", err)
		}
		rec, err := recvDB.InfoRegGet(ctx, receiver, dims)
		if err != nil || rec["Значение"] != validID {
			t.Fatalf("локальный отказ изменил запись: rec=%v err=%v", rec, err)
		}
		result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Applied != 0 || result.Skipped != len(cases) {
			t.Fatalf("повтор пакета: result=%+v err=%v", result, err)
		}
	})
}
