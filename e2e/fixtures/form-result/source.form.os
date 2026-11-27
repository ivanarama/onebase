Функция DisplayTitle()
    Возврат "MASK***";
КонецФункции

Процедура Accept()
    Объект.Записать();
    Объект.DisplayText = "computed after the durable save";
    ОткрытьФормуРезультата("Completion", Объект.Ссылка, "Record");
КонецПроцедуры

Процедура BeforeClose(Отказ)
    Если Объект.Комментарий = "mutate-on-close" Тогда
        Объект.Комментарий = "unsaved close mutation";
    КонецЕсли;
    Если Объект.Block Тогда
        Отказ = Истина;
    КонецЕсли;
КонецПроцедуры
