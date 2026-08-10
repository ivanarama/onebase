// Демо реквизита-картинки (type: image) в справочнике Номенклатура.
//
// Четыре цветные плашки-«категории» зашиты в модуль как Base64 — пример
// самодостаточен, внешних файлов не нужно. СохранитьКартинку кладёт бинарник
// в хранилище базы и возвращает UUID; именно UUID хранится в реквизите Фото,
// а показывает его форма и плитка списка. Третий аргумент связывает бинарник
// с сущностью-владельцем, поэтому /image/{uuid} применяет те же права доступа.
//
// Идемпотентность и конкурентный запуск: каждая позиция повторно проверяется
// под блокировкой, а картинка и ссылка на неё записываются в одной транзакции.
// При обычной ошибке Объект.Записать() обработчик откатывает строку _blobs и
// запускает компенсационное удаление внешнего содержимого disk/S3. Это не
// строгая атомарность внешнего хранилища: аварийное завершение процесса или
// сбой удаления S3 всё ещё требуют операционного контроля.

Функция КатегорияПоНаименованию(Наим)
  Н = НРег(Наим);
  Если СтрНайти(Н, "доска") > 0 Или СтрНайти(Н, "брус") > 0 Или СтрНайти(Н, "фанера") > 0 Тогда
    Возврат "дерево";
  ИначеЕсли СтрНайти(Н, "гвоздь") > 0 Или СтрНайти(Н, "саморез") > 0 Или СтрНайти(Н, "дюбель") > 0 Или СтрНайти(Н, "анкер") > 0 Тогда
    Возврат "крепёж";
  ИначеЕсли СтрНайти(Н, "петля") > 0 Или СтрНайти(Н, "замок") > 0 Или СтрНайти(Н, "ручка") > 0 Тогда
    Возврат "фурнитура";
  КонецЕсли;
  Возврат "инструмент";
КонецФункции

Процедура Выполнить()

  Запрос = Новый Запрос;
  Запрос.Текст = "ВЫБРАТЬ Ссылка, Наименование, Фото ИЗ Справочник.Номенклатура УПОРЯДОЧИТЬ ПО Наименование";
  Результат = Запрос.Выполнить();

  БезФото = Новый Массив;
  Для Каждого Стр Из Результат Цикл
    Если ЗначениеЗаполнено(Стр.Фото) = Ложь Тогда
      БезФото.Добавить(Стр);
    КонецЕсли;
  КонецЦикла;

  Если БезФото.Количество() = 0 Тогда
    Сообщить("У всей номенклатуры фото уже проставлено — новых картинок не сохраняю.");
    Возврат;
  КонецЕсли;

  // Плашки создаются лениво: если без фото осталась только одна категория,
  // остальные три блоба не создаются. Внутри категории UUID общий для всех
  // позиций этого запуска.
  ФотоДерево = "";
  ФотоКрепёж = "";
  ФотоФурнитура = "";
  ФотоИнструмент = "";

  Счётчик = 0;
  Для Каждого Стр Из БезФото Цикл
    // Блокировка берётся до повторного чтения. В одном процессе это mutex,
    // внутри PostgreSQL-транзакции — ещё и advisory lock с тем же ключом.
    Блокировка = БлокировкаДанных();
    ЭлементБлокировки = Блокировка.Добавить("ДемоФотоНоменклатуры");
    ЭлементБлокировки.УстановитьЗначение("Ссылка", Строка(Стр.Ссылка));

    НачатьТранзакцию();
    Попытка
      Блокировка.Заблокировать();

      // Колонка «Ссылка» в результате запроса — UUID-строка, а не ссылка с
      // менеджером, поэтому объект берём через НайтиПоИдентификатору.
      Реф = Справочники.Номенклатура.НайтиПоИдентификатору(Строка(Стр.Ссылка));
      Если Реф = Неопределено Тогда
        ОтменитьТранзакцию();
        Блокировка.Разблокировать();
        Продолжить;
      КонецЕсли;
      Объект = Реф.ПолучитьОбъект();

      // Второй параллельный запуск мог заполнить Фото, пока мы ждали lock.
      Если ЗначениеЗаполнено(Объект.Фото) Тогда
        ОтменитьТранзакцию();
        Блокировка.Разблокировать();
        Продолжить;
      КонецЕсли;

      Кат = КатегорияПоНаименованию(Объект.Наименование);
      Если Кат = "дерево" Тогда
        Если ЗначениеЗаполнено(ФотоДерево) = Ложь Тогда
          ФотоДерево = СохранитьКартинку(ПлашкаДерево(), "image/png", Реф);
        КонецЕсли;
        УУИД = ФотоДерево;
      ИначеЕсли Кат = "крепёж" Тогда
        Если ЗначениеЗаполнено(ФотоКрепёж) = Ложь Тогда
          ФотоКрепёж = СохранитьКартинку(ПлашкаКрепёж(), "image/png", Реф);
        КонецЕсли;
        УУИД = ФотоКрепёж;
      ИначеЕсли Кат = "фурнитура" Тогда
        Если ЗначениеЗаполнено(ФотоФурнитура) = Ложь Тогда
          ФотоФурнитура = СохранитьКартинку(ПлашкаФурнитура(), "image/png", Реф);
        КонецЕсли;
        УУИД = ФотоФурнитура;
      Иначе
        Если ЗначениеЗаполнено(ФотоИнструмент) = Ложь Тогда
          ФотоИнструмент = СохранитьКартинку(ПлашкаИнструмент(), "image/png", Реф);
        КонецЕсли;
        УУИД = ФотоИнструмент;
      КонецЕсли;

      Объект.Фото = УУИД;
      Объект.Записать();
    Исключение
      Ошибка = ОписаниеОшибки();
      ОтменитьТранзакцию();
      Блокировка.Разблокировать();
      ВызватьИсключение(Ошибка);
    КонецПопытки;

    // Commit держит lock до окончательного исхода БД. У commit-ошибки
    // транзакция уже завершена storage-слоем, поэтому здесь только unlock.
    Попытка
      ЗафиксироватьТранзакцию();
    Исключение
      Ошибка = ОписаниеОшибки();
      Блокировка.Разблокировать();
      ВызватьИсключение(Ошибка);
    КонецПопытки;
    Блокировка.Разблокировать();
    Счётчик = Счётчик + 1;
  КонецЦикла;

  Сообщить("Фото проставлено позициям: " + Строка(Счётчик));
  Сообщить("Откройте список «Номенклатура» и включите режим ▦ «Плитка».");

КонецПроцедуры

// ─── Демо-картинки (PNG 96x96, Base64) ──────────────────────────────────────

Функция ПлашкаДерево()
  Возврат "iVBORw0KGgoAAAANSUhEUgAAAGAAAABgCAYAAADimHc4AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAKmSURBVHhe7ZyhrtVAFEX5sGfRaAyGBIkkKASWBFAEEtQTGBIMAYWAJwkGHFgQKH7hkkPapJns2zltOd0VayfL9aRzz5rbTqfvvmuPbl2cwAcCzCDADALMIMAMAswgwAwCzCDADALMIMAMAswgwAwCzCDADALMIMAMAswgwAwCzCDADALMIMAMAswgwAwCzCDAzGEFXL1+cfrx5VMZL+/dlOfdm8MKiCZV5vLBbXnevUGAmcMKqA4COlQHAR2qg4AZojnVQcAMCDDz4fLx0CadX9+//mug4t3zh8NR84lj1bn35pAC4iFsLrFEVXVBNDYTBMzQewZAQDG9xCVK1QUI2MjTO9eHFp1PXOdVbYCAjWQaONc8BGyktwKKxLdE1QYI2EhmE07VjSBgI718+/hW1o0gYAPxkqSXuRVQgIANZK7/vcYhYAOxxdCLqpuCgJVk1v+963+AgJW8eXJ/aM35zD2AjSBgJTG7e3l294asnYKAFURje/nz+6esbUHACjJ7+LFFrWpbELCCmN29ZP+QCgELyTQse/kJELCQzM03s/oZQcACMjffSGb1M4KABXx+/2pox/lkHr6mICBJdvYvbRQCkmRm/5Kb7wgCEmRnf2xPqPo5ECCIRsabrpHMuj8yrcmS2VGNxHGqXo2/kl0EZPb5jxI1/koQ0ESNvxIENFHjrwQBTdT4K0FAEzX+ShDQRI2/EgQ0UeOv5BAC5n5w8T/JvPhR46/kEAL2egAKCb2oukoQ0ETVVYKAJqquEgQ0UXWVIKCJqqsEAU1UXSUIaKLqKkFAE1VXCQKaqLpKENBE1VWyi4B4Bxwf/hx7/f+2+A2COv8UVVfJLgLgPAgwgwAzCDCDADMIMIMAMwgwgwAzCDCDADMIMIMAMwgwgwAzCLBycfoL0SRwu1/6+6sAAAAASUVORK5CYII=";
КонецФункции

Функция ПлашкаКрепёж()
  Возврат "iVBORw0KGgoAAAANSUhEUgAAAGAAAABgCAYAAADimHc4AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAKhSURBVHhe7Zq9TSRBEEbx8UmAAEjgAiACEiACMiCEi+CEhIOBg4OFgwMO3llIGGfinnH8w7KFGGkQvbtdJ7GvqvhKelpnukbaN/PNbPWu/Pu1OhEcEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAjATAhBDxc7E6e/hzN5e5kq7l2Jvtrk8fLvWavFnZ8s88XE0KAfQGL6v58p7l2Fia1t+5Pt5s9lkFJAbdHP95XLa63q7/RY1nUEzCNkpe/V++rFhcVPQPlBHiix+6UVo9lUkqAJ3q8z5Svoo4AR/TQuT+mjABP9NwcrDd7EJQQ4Imeu+PNZg+K/AIc0WN3SbMHSHoBvdHzfH3WXE+TWoAneiLl/pi8AhzR454jLZG0Anqj5+H3zw/ropFSQG/0vOU+PGpYRD4Bjui5Odz4dK5opBPQGz3kiNlDKgG90WMbMa3zRCSVgJ7jLJ6i5/6YNAJ6jrHKkPtj0gjorfHbUgbSCLBc761Md0G5Z4BVpudAKgF2ZfdWljehVALsWPvsrcgzoIF0AgwbMfRW9OdBSgGeKIo+D0opwLBRQ29F3AkbSCugd91Q0faCB1ILsF0uT0XcFUstwPBEkZ2n1YMkvQDDE0Xz+hCUEOCNIhtrt/oQlBBgeKIo0qiijADDE0VRRhWlBNhV7akI25a1BEyx931P0aOKcgIMz94BPaooKcAbReSft2oKmOKNImpUUVaA4YkiK2JUEUKAPQjtx9E8/uvLmUZRq9csvq2A74wEwEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAjATASACMBMBIAIwEwEgAyurkFXcib84KhU4DAAAAAElFTkSuQmCC";
КонецФункции

Функция ПлашкаФурнитура()
  Возврат "iVBORw0KGgoAAAANSUhEUgAAAGAAAABgCAYAAADimHc4AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAAM6SURBVHhe7ZkxbhRBFEQ5ATkX8AE4AAfgBL6AT8AJOAJHICAgIXBC4oQAEhIiIkdkNliAZSyEkAaVtS01o7+71db0r97dKull89uz83r697QfPHx1MRkdFiDGAsRYgBgLEGMBYixAjAWIsQAxFiDGAsRYgBgLEGMBYixAjAWIsQAxFiDGAsRYgBgLEGMBYixAjAWIsQAxFiBmpwScfvm9lahuZHZKAJOobmQsQIwFiLEAMcMJeHz6bTp+92N69vF6enl++1+DZVJfj3qMg/EwbvT31AwhAA/n+aeb6fz67+ox9suLz7+GkiEVcPTmKz2zlw7+7ggiZAJO3v9cPQptsERF95eFRACWgZGC+4nuM4N0AaM9/BKVhFQB2I2MHMVylCYADbc12EaiVzx5e3UHk3It6lDfmuzGnCagZen5cPnnTth8DCbzmtadFq6dj9GTFAEts3/TWswkqgMtEyCS34sUAeyWEx9ij15fhmMAJlFdAW8WE3wURvU9SBHALgFYu6P6ApOorsD2EYiK6nuQIoAJZn9UW8Mkqqth34KotgfdBWBJYcK89kyiuhpsNZlk7Ya6C2Bfe/SJqL6GSVRX8/Ts++rKzdm2HC7FMAKYH8wkqqtZ8n6WwALW5OAE4Jgiqq9hEtXVHJwAtgkz5zBMorqag2vCgAlzBMAkqqthz4ei2h6kCGB/9LYjACZRXYF9GzPPg1IEsEcR274FmER1BXb5YbbES5EigJ15yKbmxySqA2zzRfbuMA60nEauk8Akqmt5+FguozF6kSag5UgagbD5ToRJfT3qW8QjmbMfpAkAbC+YB02RPVFtuXaevf6XZKF1RmYlc+dTky4ADZk9Es4K7gf3Fd1vb9IFAPzYUd4ENF3VwwcSAYX79oSlkrnfX4dUAMDsYz+Qlgo++JSzvkYuoAb/LMHStHSPwHgYF+NHf1fJUALmYE+Oj6gCk/r67D39fRhawBwmUd3IWIAYCxBjAWJ2SkDdYNcR1Y3MTgnYRyxAjAWIsQAxFiDGAsRYgBgLEGMBYixAjAWIsQAxFiDGAsRYgBgLEGMBYixAjAWIsQAxFiDGAsRYgBgLEGMBYixAjAVIuZj+AV8JlooQxlIwAAAAAElFTkSuQmCC";
КонецФункции

Функция ПлашкаИнструмент()
  Возврат "iVBORw0KGgoAAAANSUhEUgAAAGAAAABgCAYAAADimHc4AAAAAXNSR0IArs4c6QAAAARnQU1BAACxjwv8YQUAAAAJcEhZcwAADsMAAA7DAcdvqGQAAALZSURBVHhe7ZkxihRRGIQ9grkH8AIewAN4AS/gCbzAmhiZiJGZRkYmgiAoCIIbCBtsoCCCCCYGYmQ88qR/aMea6f+9Hqrea6rgy7r+hflma3eYK1dfne2MDgsQYwFiLECMBYixADEWIMYCxFiAGAsQYwFiLECMBYixADEWIMYCxFiAGAsQYwFiLECMBYixADEWIMYCxFiAGAsQYwFi6AJuvHu0e/Hj01FuXzyD3Vpunj+G9xGoz4AuoLwoS7n78SXs1nL+69t0cTmoz2CzAsqNmqAbDDYpoMxcbdAdBpsUUDM9EXSHweYE1E5PBN1isCkBLdMTQfcYbEpAy/RE0D0GmxHQOj0RdJPBJgRcf/tgarYH3WWwCQHlk+zaoLsMhhdw5/L51FoXdJvB0AKy05P544zuMxhaQGZ6yq3Mc+g+g2EFZKanvPPLsxYw4xQCstNTPpiV5y1gxikEPP1+MT15OPMbFjBjrYBbH55MTx1OTE9gATPWCLj2+v70xPGUnzHv+b+gGWsEZKbn3uc3//Uy2e+wGEZAZnq+/P7597dkv5vJfofFEAJapyfIBPUYDCGgdXqCTFCPQfcC1kxPkAnqMehaQHlRy4u7lEPTE2SCegy6FlBmZSnHpifIBPUYdCsg89zS9ASZoB6DLgWUd/UppifIBPUYdCkg8+JnpifIBPUYdClgKdnpCTJBPQZDCshOT5AJ6jEYTkDN9ASZoB6DoQTUTk+QCeoxGEpA7fQEmaAeg2EEtExPkAnqMRhCQOv0BJmgHoMhBLROT5AJ6jHoXsDDr+/hnRoyQT0G3QtYMz1BJqjHoGsB5bsAdKOWTFCPQbcCyrdgqN9CJqjHgC6gTEqRsMQppidA9/dBPQZ0AeZfLECMBYixADEWIMYCxFiAGAsQYwFiLECMBYixADEWIMYCxFiAGAsQYwFiLECMBYixADEWIMYCxFiAGAsQYwFiLECMBUg52/0Be3jXl+R04tQAAAAASUVORK5CYII=";
КонецФункции
