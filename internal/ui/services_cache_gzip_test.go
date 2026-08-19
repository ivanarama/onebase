package ui

// Заявка #1004: связка кэш + gzip не покрывалась ни одним тестом. Сервисы в
// кэш-тестах объявлены с auth: none, то есть сжатие у них включено по
// умолчанию, — но Accept-Encoding не слал никто. Заявленный дизайн «в кэше
// лежит НЕсжатое тело, сжатие применяется на выдаче» проверять было нечем:
// регрессия, при которой в кэш попадёт уже сжатое тело, видна только клиенту
// без gzip — ему уедет бинарный мусор вместо страницы.
//
// Берётся /hs/novary/big: 3000 байт — больше порога сжатия (1 КиБ) и меньше
// предела кэшируемого тела (1 МиБ по умолчанию), то есть ответ одновременно и
// кэшируемый, и сжимаемый.

import (
	"strings"
	"testing"
)

const cacheGzipPath = "/hs/novary/big"

func TestServiceCache_GzipAppliedOnDelivery(t *testing.T) {
	t.Run("прогрев без сжатия — попадание отдаётся сжатым", func(t *testing.T) {
		c := newCacheTestServer(t)

		plain := c.get(t, cacheGzipPath)
		if enc := plain.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("прогрев: Content-Encoding=%q при клиенте без gzip", enc)
		}
		if plain.Body.Len() != 3000 {
			t.Fatalf("прогрев: тело %d байт, ожидалось 3000", plain.Body.Len())
		}

		zipped := c.get(t, cacheGzipPath, "Accept-Encoding", "gzip")
		if enc := zipped.Header().Get("Content-Encoding"); enc != "gzip" {
			t.Fatalf("ответ из кэша не сжат: Content-Encoding=%q", enc)
		}
		if v := zipped.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
			t.Errorf("Vary=%q — без него общий кэш отдаст сжатое тело клиенту без gzip", v)
		}
		if body := gunzip(t, zipped); len(body) != 3000 {
			t.Errorf("после распаковки %d байт, ожидалось 3000", len(body))
		}
		if got := c.calls(); got != 1 {
			t.Errorf("обработчик вызван %d раз(а): второй ответ должен был прийти из кэша", got)
		}
	})

	t.Run("прогрев сжатым — клиент без gzip получает текст, а не мусор", func(t *testing.T) {
		c := newCacheTestServer(t)

		zipped := c.get(t, cacheGzipPath, "Accept-Encoding", "gzip")
		if enc := zipped.Header().Get("Content-Encoding"); enc != "gzip" {
			t.Fatalf("прогрев: Content-Encoding=%q, ожидался gzip", enc)
		}
		if body := gunzip(t, zipped); len(body) != 3000 {
			t.Fatalf("прогрев: после распаковки %d байт, ожидалось 3000", len(body))
		}

		plain := c.get(t, cacheGzipPath)
		if enc := plain.Header().Get("Content-Encoding"); enc != "" {
			t.Fatalf("клиенту без gzip уехал Content-Encoding=%q — в кэше лежит сжатое тело", enc)
		}
		if plain.Body.Len() != 3000 {
			t.Fatalf("клиенту без gzip уехало %d байт вместо 3000 — похоже, из кэша достали сжатое тело",
				plain.Body.Len())
		}
		if strings.Contains(plain.Body.String()[:2], "\x1f\x8b") {
			t.Fatal("клиенту без gzip уехал gzip-поток (сигнатура 1f 8b)")
		}
		if got := c.calls(); got != 1 {
			t.Errorf("обработчик вызван %d раз(а): второй ответ должен был прийти из кэша", got)
		}
	})
}
