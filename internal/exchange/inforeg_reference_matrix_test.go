package exchange_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

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

// Измерение — часть NOT NULL ключа, а не nullable ресурс. Обмен сохраняет
// прежнее написание SQLite-ключа для последующего upsert и tombstone;
// PostgreSQL по-прежнему отвергает непредставимый UUID на границе СУБД.
func TestApplyPackageInfoRegReferenceDimensionMatrix(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, recvDB *storage.DB) {
		ctx := context.Background()
		sendDB, sendCtx, sender := newInfoRegBase(t)
		receiver := infoRegCodes()
		receiver.Dimensions[0].Type = "reference:Товар"
		receiver.Dimensions[0].RefEntity = "Товар"
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
		sendResolver := fakeResolverIR{inforegs: map[string]*metadata.InfoRegister{sender.Name: sender}}
		recvResolver := fakeResolverIR{inforegs: map[string]*metadata.InfoRegister{receiver.Name: receiver}}
		keys := []string{uuid.New().String(), "ПОС-00001", "ПОС-00002"}
		for _, key := range keys {
			dims := map[string]any{"Ключ": key}
			if err := sendDB.InfoRegSet(sendCtx, sender, dims, map[string]any{"Значение": "первое"}, nil); err != nil {
				t.Fatal(err)
			}
			if err := exchange.RegisterInfoRegOnSave(sendCtx, sendDB, []*metadata.ExchangePlan{plan}, sender, dims, false); err != nil {
				t.Fatal(err)
			}
		}
		build := func() []byte {
			t.Helper()
			data, err := exchange.BuildPackage(sendCtx, sendDB, sendResolver, plan, "fil01")
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
		data := build()
		result, err := exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if !recvDB.IsSQLite() {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "22P02" {
				t.Fatalf("прежний отказ PostgreSQL для строкового UUID: result=%+v err=%v", result, err)
			}
			rows, queryErr := recvDB.InfoRegList(ctx, receiver, storage.RegFilter{})
			if queryErr != nil || len(rows) != 0 {
				t.Fatalf("отклонённый пакет должен откатиться: rows=%v err=%v", rows, queryErr)
			}
			// Убираем несовместимые записи из исходящего пакета, чтобы проверить
			// полный цикл корректной ссылки и на PostgreSQL.
			pending, pendingErr := sendDB.PendingExchangeChanges(sendCtx, plan.Name, "fil01")
			if pendingErr != nil {
				t.Fatal(pendingErr)
			}
			for _, change := range pending {
				if err := sendDB.DeleteExchangeChange(sendCtx, change.Plan, change.ObjectType, change.ObjectID, change.NodeCode); err != nil {
					t.Fatal(err)
				}
			}
			keys = keys[:1]
			if err := exchange.RegisterInfoRegOnSave(sendCtx, sendDB, []*metadata.ExchangePlan{plan}, sender, map[string]any{"Ключ": keys[0]}, false); err != nil {
				t.Fatal(err)
			}
			data = build()
			result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		}
		if err != nil || result.Applied != len(keys) {
			t.Fatalf("пакет со ссылочным измерением: result=%+v err=%v", result, err)
		}
		for _, key := range keys {
			rec, err := recvDB.InfoRegGet(ctx, receiver, map[string]any{"Ключ": key})
			if err != nil || rec["Ключ"] != key || rec["Значение"] != "первое" {
				t.Fatalf("ключ %q изменён или потерян: rec=%v err=%v", key, rec, err)
			}
		}
		result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Applied != 0 || result.Skipped != len(keys) {
			t.Fatalf("повтор пакета: result=%+v err=%v", result, err)
		}
		if err := recvDB.InfoRegSet(ctx, receiver, map[string]any{"Ключ": "ПОС-00001"}, map[string]any{"Значение": "локальное"}, nil); !errors.Is(err, storage.ErrReferenceTypeMismatch) {
			t.Fatalf("локальное представление должно отклоняться: %v", err)
		}
		unchanged, err := recvDB.InfoRegList(ctx, receiver, storage.RegFilter{})
		if err != nil || len(unchanged) != len(keys) {
			t.Fatalf("локальный отказ изменил набор записей: rows=%v err=%v", unchanged, err)
		}
		for _, rec := range unchanged {
			if rec["Значение"] != "первое" {
				t.Fatalf("локальный отказ изменил ресурс: %v", rec)
			}
		}

		// Более поздний пакет обновляет те же ключи, а не создаёт другие строки.
		pending, err := sendDB.PendingExchangeChanges(sendCtx, plan.Name, "fil01")
		if err != nil || len(pending) != len(keys) {
			t.Fatalf("исходящие ключи: pending=%v err=%v", pending, err)
		}
		for _, change := range pending {
			change.Version, change.ChangedAt = farFuture, farFuture
			if err := sendDB.RegisterExchangeChange(sendCtx, change); err != nil {
				t.Fatal(err)
			}
		}
		for _, key := range keys {
			if err := sendDB.InfoRegSet(sendCtx, sender, map[string]any{"Ключ": key}, map[string]any{"Значение": "второе"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, build(), exchange.ApplyOptions{})
		if err != nil || result.Applied != len(keys) {
			t.Fatalf("обновление по исходному ключу: result=%+v err=%v", result, err)
		}
		rows, err := recvDB.InfoRegList(ctx, receiver, storage.RegFilter{})
		if err != nil || len(rows) != len(keys) {
			t.Fatalf("число строк после upsert: rows=%v err=%v", rows, err)
		}
		for _, key := range keys {
			rec, err := recvDB.InfoRegGet(ctx, receiver, map[string]any{"Ключ": key})
			if err != nil || rec["Значение"] != "второе" {
				t.Fatalf("обновление %q: rec=%v err=%v", key, rec, err)
			}
			if err := sendDB.InfoRegDelete(sendCtx, sender, map[string]any{"Ключ": key}, nil); err != nil {
				t.Fatal(err)
			}
		}
		for _, change := range pending {
			change.Deletion = true
			change.Version, change.ChangedAt = farFuture+1, farFuture+1
			if err := sendDB.RegisterExchangeChange(sendCtx, change); err != nil {
				t.Fatal(err)
			}
		}
		data = build()
		result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Deleted != len(keys) {
			t.Fatalf("tombstone по исходному ключу: result=%+v err=%v", result, err)
		}
		rows, err = recvDB.InfoRegList(ctx, receiver, storage.RegFilter{})
		if err != nil || len(rows) != 0 {
			t.Fatalf("tombstone оставил записи: rows=%v err=%v", rows, err)
		}
		result, err = exchange.ApplyPackage(ctx, recvDB, recvResolver, plan, data, exchange.ApplyOptions{})
		if err != nil || result.Deleted != 0 || result.Skipped != len(keys) {
			t.Fatalf("повтор tombstone: result=%+v err=%v", result, err)
		}
	})
}
