package selfupdate

// Обнаружение подписи среди ассетов релиза (#967).
//
// Это единственное место, где подпись вообще находится: `toRelease` сопоставляет
// имя ассета и заполняет SigURL. Тестов на него не было, и последствие
// асимметричное — испорти имя здесь, и весь набор проверок подписи останется
// зелёным, потому что все они конструируют Release{SigURL: …} литералом. SigURL
// окажется пустым, мягкий режим сочтёт релиз неподписанным, и проверка тихо
// выключится целиком.
//
// С `.sha256` так не выйдет: без суммы toRelease возвращает ошибку. Подпись же
// в мягком режиме «необязательна» — ровно поэтому её потеря и проходит молча,
// и ровно поэтому здесь нужен страж.

import (
	"context"
	"testing"
)

func TestLatestRelease_ПодписьНаходитсяСредиАссетов(t *testing.T) {
	asset := AssetBaseName()
	srv := githubMock(t,
		ghRelJSON("v1.0.0", false, asset, asset+".sha256", asset+".sha256.sig"),
		nil,
	)

	rel, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.SigURL == "" {
		t.Fatal("SigURL пуст, хотя ассет подписи в релизе есть — " +
			"обновление сочло бы релиз неподписанным и пропустило его в мягком режиме")
	}
	if rel.SHAURL == "" {
		t.Error("SHAURL пуст при наличии ассета суммы")
	}
}

// Релиз без подписи — законное состояние на время мягкого перехода: SigURL
// пуст, но сам релиз пригоден. Проверка нужна, чтобы страж выше не «чинили»
// требованием подписи там, где её пока может не быть.
func TestLatestRelease_БезПодписиРелизВсёРавноПригоден(t *testing.T) {
	asset := AssetBaseName()
	srv := githubMock(t,
		ghRelJSON("v1.0.0", false, asset, asset+".sha256"),
		nil,
	)

	rel, err := latestReleaseFrom(context.Background(), srv.URL, "acme/onebase", ChannelStable)
	if err != nil {
		t.Fatalf("LatestRelease: %v", err)
	}
	if rel.SigURL != "" {
		t.Errorf("SigURL = %q, хотя ассета подписи в релизе нет", rel.SigURL)
	}
	if rel.AssetURL == "" {
		t.Error("релиз без подписи должен оставаться пригодным для обновления")
	}
}
