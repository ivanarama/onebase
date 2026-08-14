package auth

import (
	"testing"
	"time"
)

// Карта начатых входов через внешнего провайдера росла неограниченно, а начать
// вход может кто угодно: GET /auth/oidc/<id>/start не требует аутентификации и
// не ограничен по частоте. Плюс чистка обходила карту ЦЕЛИКОМ на каждой
// вставке под общим мьютексом — вставка дорожала линейно, а входы вставали в
// очередь на этот же лок (#615).

func resetOIDCStates() {
	oidcStatesMu.Lock()
	oidcStates = map[string]*oidcState{}
	oidcLastSweep = time.Time{}
	oidcStatesDrops = 0
	oidcStatesScans = 0
	oidcStatesMu.Unlock()
}

// Потолок соблюдается, даже когда протухших записей нет вовсе.
func TestPutOIDCState_ПотолокСоблюдается(t *testing.T) {
	resetOIDCStates()
	t.Cleanup(resetOIDCStates)

	for i := 0; i < maxOIDCStates+500; i++ {
		putOIDCState(randomStateKey(i), &oidcState{expires: time.Now().Add(oidcStateTTL)})
	}
	if n := oidcStateCount(); n > maxOIDCStates {
		t.Fatalf("в карте %d записей, потолок %d", n, maxOIDCStates)
	}
	if oidcStatesDrops == 0 {
		t.Error("вытеснения не было — потолок держится не тем механизмом, что задуман")
	}
}

// На потолке проход по карте выполняется не на каждой вставке.
//
// Повод — #863: гейт чистки стоял через ИЛИ («вышел интервал ИЛИ карта на
// потолке»), а под устойчивым флудом карта на потолке находится ПОСТОЯННО.
// Значит, полный обход шёл на каждом запросе логина, да ещё и вытеснение
// искало старейшего заново для каждой удаляемой записи: два полных обхода
// карты на 10 000 записей под общим мьютексом. Защита от роста памяти
// работала усилителем нагрузки на CPU.
//
// Тест считает не время (это было бы флаки), а число проходов вытеснения:
// освобождая место пачкой, следующие oidcEvictBatch вставок обязаны пройти
// вообще без обходов.
func TestPutOIDCState_НаПотолкеНеСканируетНаКаждойВставке(t *testing.T) {
	resetOIDCStates()
	t.Cleanup(resetOIDCStates)

	// Забиваем карту доверху заведомо живыми записями: чистка по протухшим
	// освободить ничего не сможет, останется только вытеснение.
	for i := 0; i < maxOIDCStates; i++ {
		putOIDCState(randomStateKey(i), &oidcState{expires: time.Now().Add(oidcStateTTL)})
	}

	oidcStatesMu.Lock()
	oidcStatesScans = 0
	oidcStatesMu.Unlock()

	const extra = 3 * oidcEvictBatch
	for i := 0; i < extra; i++ {
		putOIDCState(randomStateKey(maxOIDCStates+i), &oidcState{expires: time.Now().Add(oidcStateTTL)})
	}

	oidcStatesMu.Lock()
	passes := oidcStatesScans
	oidcStatesMu.Unlock()

	// Пачка освобождает oidcEvictBatch мест, поэтому на 3 пачки вставок нужно
	// около 3 проходов. Прежний код делал ровно extra проходов — по одному на
	// вставку; порог с запасом отделяет одно от другого.
	if passes == 0 {
		t.Fatal("ни одного прохода вытеснения — тест не отличает новую реализацию от старой без счётчика")
	}
	if maxPasses := extra/oidcEvictBatch + 2; passes > maxPasses {
		t.Errorf("%d проходов вытеснения на %d вставок (ожидалось не больше %d) — "+
			"карта сканируется на каждой вставке, как до #863", passes, extra, maxPasses)
	}
	if n := oidcStateCount(); n > maxOIDCStates {
		t.Errorf("потолок нарушен: %d записей при потолке %d", n, maxOIDCStates)
	}
}

// Вытеснение удаляет именно n старейших, а не произвольные n элементов map.
// Проверка одного долгожителя была вероятностной: при случайном удалении 10%
// он всё равно выживал примерно в девяти запусках из десяти.
func TestEvictOldestOIDCStates_УдаляютсяИменноСтарейшие(t *testing.T) {
	resetOIDCStates()
	t.Cleanup(resetOIDCStates)

	now := time.Now()
	oidcStatesMu.Lock()
	oidcStates = map[string]*oidcState{
		"старейший": {expires: now.Add(time.Minute)},
		"старый":    {expires: now.Add(2 * time.Minute)},
		"свежий":    {expires: now.Add(3 * time.Minute)},
		"новейший":  {expires: now.Add(4 * time.Minute)},
	}
	evictOldestOIDCStates(2)
	_, oldestExists := oidcStates["старейший"]
	_, oldExists := oidcStates["старый"]
	_, freshExists := oidcStates["свежий"]
	_, newestExists := oidcStates["новейший"]
	oidcStatesMu.Unlock()

	if oldestExists || oldExists {
		t.Errorf("старейшие записи пережили вытеснение: старейший=%v старый=%v", oldestExists, oldExists)
	}
	if !freshExists || !newestExists {
		t.Errorf("вытеснены свежие записи: свежий=%v новейший=%v", freshExists, newestExists)
	}
}

// Протухшие вычищаются, а не вытесняются: под давлением сначала уходят они.
func TestPutOIDCState_ПротухшиеУходятПервыми(t *testing.T) {
	resetOIDCStates()
	t.Cleanup(resetOIDCStates)

	putOIDCState("протухший", &oidcState{expires: time.Now().Add(-time.Minute)})
	putOIDCState("живой", &oidcState{expires: time.Now().Add(oidcStateTTL)})
	// Вторая вставка идёт сразу за первой, поэтому чистка по интервалу могла не
	// сработать: подвинем отметку и вставим ещё одну.
	oidcStatesMu.Lock()
	oidcLastSweep = time.Now().Add(-2 * oidcSweepInterval)
	oidcStatesMu.Unlock()
	putOIDCState("третий", &oidcState{expires: time.Now().Add(oidcStateTTL)})

	if _, ok := takeOIDCState("протухший"); ok {
		t.Error("протухшая запись не вычищена")
	}
	if _, ok := takeOIDCState("живой"); !ok {
		t.Error("живая запись потеряна")
	}
}

// Одноразовость сохранена: забрали — больше не отдаём.
func TestTakeOIDCState_Одноразовость(t *testing.T) {
	resetOIDCStates()
	t.Cleanup(resetOIDCStates)

	putOIDCState("s", &oidcState{expires: time.Now().Add(oidcStateTTL)})
	if _, ok := takeOIDCState("s"); !ok {
		t.Fatal("первое обращение должно отдавать состояние")
	}
	if _, ok := takeOIDCState("s"); ok {
		t.Error("состояние отдано повторно")
	}
}

func randomStateKey(i int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, 0, 12)
	for n := i; ; n /= 16 {
		b = append(b, digits[n%16])
		if n < 16 {
			break
		}
	}
	return "state-" + string(b)
}
