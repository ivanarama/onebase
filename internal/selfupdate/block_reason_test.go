package selfupdate

// Причина, по которой самообновление недоступно, и сопоставимость версии
// (#1065). И то и другое — вход интерфейса: по классу причины выбирается совет
// пользователю, а по сопоставимости — формулировка про версию.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateBinaryUpdateTarget_MissingDirIsNotWritable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "нет-такого-каталога")
	err := ValidateBinaryUpdateTarget(missing)
	if !errors.Is(err, ErrTargetNotWritable) {
		t.Fatalf("ошибка = %v, ожидалась ErrTargetNotWritable", err)
	}
	if got := ClassifyTargetBlock(err); got != TargetBlockNotWritable {
		t.Errorf("класс причины = %q, ожидался %q", got, TargetBlockNotWritable)
	}
	// Путь в тексте: без него пользователь не поймёт, о каком каталоге речь.
	if !errors.Is(err, ErrTargetNotWritable) || len(err.Error()) <= len(ErrTargetNotWritable.Error()) {
		t.Errorf("в отказе нет пути установки: %v", err)
	}
}

func TestClassifyTargetBlock(t *testing.T) {
	cases := map[string]struct {
		err  error
		want TargetBlock
	}{
		"нет причины":        {nil, TargetBlockNone},
		"нет прав":           {errors.New("x: " + ErrTargetNotWritable.Error()), TargetBlockOther},
		"обёрнутые права":    {errors.Join(ErrTargetNotWritable, errors.New("деталь")), TargetBlockNotWritable},
		"общая установка":    {errors.Join(ErrTargetNotPrivate, errors.New("деталь")), TargetBlockNotPrivate},
		"неизвестная ошибка": {errors.New("каталог исчез"), TargetBlockOther},
	}
	for name, tc := range cases {
		if got := ClassifyTargetBlock(tc.err); got != tc.want {
			t.Errorf("%s: класс = %q, ожидался %q", name, got, tc.want)
		}
	}
}

// Владелец приватного каталога обновляться может — иначе правка запретила бы
// нормальный случай.
func TestValidateBinaryUpdateTarget_PrivateDirIsAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: 0700 у каталога — это и есть «личная установка», ради которой тест
		t.Fatal(err)
	}
	if err := ValidateBinaryUpdateTarget(dir); err != nil {
		t.Fatalf("личный каталог отклонён: %v", err)
	}
}

func TestVersionRecognized(t *testing.T) {
	recognized := []string{"build-0", "build-793", "v0.9.8", "v0.10.0", "v0.9.8-rc1"}
	for _, v := range recognized {
		if !VersionRecognized(v) {
			t.Errorf("%q не распознана, хотя это штатная схема выпусков", v)
		}
	}
	// Ровно эти версии видел пользователь в #1065: страница называла их
	// актуальными, хотя обновление им не предложат никогда.
	unknown := []string{"build-793fix", "dev-3c79f25e", "dev", "", "0.9.8", "release-5"}
	for _, v := range unknown {
		if VersionRecognized(v) {
			t.Errorf("%q объявлена сопоставимой с выпусками — Newer по ней всегда отказывает", v)
		}
		if Newer(v, "build-100000") {
			t.Errorf("Newer(%q, build-100000) = true — тест противоречит сам себе", v)
		}
	}
}
