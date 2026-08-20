# Этап 145 — Публикация PWA в App Store / Google Play (нативная обёртка)

> Номер сменён с 46 на 145 (issue #1035): под 46 жили разные планы, и ссылка «план 46» перестала быть однозначной. Номер 46 остался за планом «Команды табличной части + подбор» (46-tablepart-commands-and-picker). Ссылки «план 46» на эту тему читать как «план 145».

## Контекст

Этап [45](45-mobile-pwa.md) сделал onebase устанавливаемым PWA: пользователь
заходит в браузере и добавляет приложение на домашний экран. Этого достаточно для
повседневной работы с телефона. Но PWA **не попадает в магазины приложений** —
установка идёт мимо App Store/Google Play.

Этот этап — **опциональная надстройка поверх 45**, нужна только если потребуется:
присутствие в сторах (доверие/находимость), доступ к нативным возможностям
(push на iOS, биометрия, файловая система, шеринг), либо корпоративная раздача
через MDM.

Принцип: **UI не переписывается**. Нативная оболочка лишь открывает уже
работающий сайт onebase в полноэкранном webview и оборачивает его в установочный
пакет. Вся бизнес-логика остаётся на сервере (Go SSR), как и сейчас.

Предусловие: сервер доступен по HTTPS на стабильном домене (есть —
`https://demo.ivantitov.tech`).

## Подход

Две технологии под разные платформы — берём минимально достаточную для каждой:

### Android — TWA (Trusted Web Activity) через Bubblewrap (рекомендуется)
- TWA = системный Chrome рендерит PWA на весь экран **без адресной строки и без
  webview-кода**. Пакет `.aab` для Google Play генерирует
  [Bubblewrap CLI](https://github.com/GoogleChromeLabs/bubblewrap).
- Условие «без адресной строки»: **Digital Asset Links** — связать домен и
  приложение. Нужен файл `/.well-known/assetlinks.json` на сервере с отпечатком
  подписи приложения.
- Плюсы: легче всего, переиспользует наш `manifest.webmanifest`, авто-обновление
  контента (это же сайт). Минусы: только Android; нативные API ограничены.

### iOS (и при желании Android) — Capacitor
- [Capacitor](https://capacitorjs.com/) оборачивает веб-приложение в нативный
  проект Xcode/Android Studio. Режимы:
  - `server.url` → webview грузит онлайн-сайт (как TWA, но через WKWebView);
  - либо упаковать статику локально (нам не подходит — UI серверный).
  Берём `server.url = https://<домен>`.
- Плагины Capacitor дают нативные API при необходимости (Push, Share, Camera,
  Filesystem, Biometric).
- Минусы: для iOS обязателен **Mac + Xcode** и платный Apple Developer ($99/год);
  ревью App Store строже к «просто сайту в обёртке» — нужны реальные нативные
  фишки или явная ценность.

**Рекомендация:** начать с Android/TWA (дёшево, быстро), iOS/Capacitor добавлять
только при реальной потребности в Apple-экосистеме.

## Изменения в коде onebase

Оболочки живут **в отдельном репозитории/каталоге** (не в Go-бинаре). На стороне
onebase нужен минимум:

1. **Digital Asset Links (для TWA)** — отдавать `/.well-known/assetlinks.json`.
   Добавить маршрут рядом с `mountPWA` (`internal/ui/pwa.go`/`static.go`):
   `GET /.well-known/assetlinks.json` → `application/json`. Содержимое (отпечаток
   `sha256_cert_fingerprints` подписи) появляется после генерации ключа в
   Bubblewrap — параметризовать через конфиг/файл, не хардкодить.
2. **Иконки/цвета** — уже есть из этапа 45 (`manifest.webmanifest`, 192/512 +
   maskable, `theme_color`). Дополнительно для сторов понадобятся бордерные
   ассеты (feature graphic, скриншоты) — это материалы стора, не код.
3. **(Опц.) CORS/CSP** — при `server.url` webview грузит тот же origin, доп.
   настройки обычно не нужны; проверить, что заголовки не запрещают встраивание.

Вне репозитория onebase (отдельный проект сборки):
- `android-twa/` — проект Bubblewrap (`twa-manifest.json`, ключ подписи, `.aab`).
- `ios-capacitor/` (если делаем iOS) — Capacitor-проект с `server.url`.

## Тесты

- Юнит на стороне onebase: маршрут `/.well-known/assetlinks.json` отдаёт 200 и
  валидный JSON (по образцу `internal/ui/pwa_test.go`).
- Ручная проверка связки домена: Google
  [Statement List Tester](https://developers.google.com/digital-asset-links/tools/generator)
  подтверждает assetlinks.

## Verification

1. **Android/TWA**:
   - `bubblewrap init --manifest https://<домен>/manifest.webmanifest`,
     `bubblewrap build` → получить `.aab` и отпечаток ключа.
   - Положить отпечаток в `assetlinks.json`, задеплоить onebase.
   - Установить `.aab` на устройство/эмулятор — приложение открывается
     **на весь экран без адресной строки** (если строка есть — assetlinks не
     сошлись).
   - Загрузить в Google Play Console (internal testing track).
2. **iOS/Capacitor** (если делаем):
   - `npm i @capacitor/core @capacitor/cli`, `npx cap init`,
     `server.url` в `capacitor.config`, `npx cap add ios`, открыть в Xcode,
     запустить на симуляторе/устройстве — onebase во весь экран WKWebView.
   - Прогнать через TestFlight перед App Store.
3. Проверить авто-обновление: правка на сервере видна в приложении без
   перевыпуска пакета (контент — это сайт).

## Эстимейт

- Android/TWA (Bubblewrap + assetlinks + маршрут + Play internal testing):
  **1–2 дня** (+ время на аккаунт Google Play, разовый $25).
- iOS/Capacitor (проект + сборка + TestFlight): **2–4 дня** (требует Mac/Xcode +
  Apple Developer $99/год; ревью App Store — риск по срокам).
- Маршрут `assetlinks.json` + тест в onebase: **~0.5 дня**.

## Зависимости

- Этап [45](45-mobile-pwa.md) (PWA-манифест и иконки) — ✅ обязателен.
- Стабильный HTTPS-домен — есть.
