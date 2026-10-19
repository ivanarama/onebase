// Export the original SVG demo illustrations to PNG for OneBase image fields.
// The generated DSL embeds the bytes so demo seeding needs no file permissions.
const {chromium}=require('@playwright/test');
const fs=require('node:fs');
const path=require('node:path');
const root=path.resolve(__dirname,'..');

(async()=>{
  const edge='C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';
  const executablePath=process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH ||
    (process.platform==='win32' && fs.existsSync(edge) ? edge : undefined);
  const browser=await chromium.launch({headless:true,executablePath});
  try {
    const page=await browser.newPage({viewport:{width:600,height:360},deviceScaleFactor:1});
    const lines=['// Generated from demo/art/*.svg by node e2e/render-cms-demo-art.cjs.',
      '// Original vector illustrations are part of this public demo.',
      'Функция Данные(Слаг)'];
    for(const [index,slug] of ['demo-mug','demo-notebook','demo-organizer'].entries()){
      await page.setContent('<style>html,body{margin:0;padding:0}</style>'+fs.readFileSync(path.join(root,'examples/cms/demo/art',slug+'.svg'),'utf8'));
      const bytes=await page.locator('svg').screenshot({animations:'disabled'});
      const chunks=bytes.toString('base64').match(/.{1,1200}/g);
      lines.push('    '+(index ? 'ИначеЕсли' : 'Если')+' Слаг = "'+slug+'" Тогда');
      lines.push('        Возврат '+chunks.map((s,i)=>(i?'            + ':'')+'"'+s+'"').join('\n')+';');
    }
    lines.push('    КонецЕсли;','    ВызватьИсключение "Нет демонстрационной иллюстрации: " + Слаг;','КонецФункции','',
      'Функция ПолучитьМедиа(Слаг, Сайт, Имя) Экспорт',
      '    З = Новый Запрос;',
      '    З.Текст = "ВЫБРАТЬ Ссылка ИЗ Справочник.Медиа ГДЕ Сайт = &Сайт И ВнешнийКлюч = &Ключ";',
      '    З.УстановитьПараметр("Сайт", Сайт);',
      '    З.УстановитьПараметр("Ключ", "demo-art/" + Слаг);',
      '    Для Каждого Стр Из З.Выполнить() Цикл',
      '        Возврат Справочники.Медиа.НайтиПоИдентификатору(Строка(Стр.Ссылка));',
      '    КонецЦикла;',
      '    Медиа = Справочники.Медиа.Создать();',
      '    Медиа.Наименование = "Иллюстрация: " + Имя;',
      '    Медиа.Сайт = Сайт;',
      '    Медиа.ВнешнийКлюч = "demo-art/" + Слаг;',
      '    Медиа.Альт = Имя;',
      '    Медиа.Тип = Перечисления.ТипМедиа.Изображение;',
      '    Медиа.Активен = Истина;',
      '    Медиа.Файл = СохранитьКартинку(Данные(Слаг), "image/png");',
      '    Возврат Медиа.Записать();',
      'КонецФункции','');
    const output=path.join(root,'examples/cms/src/ДемоИллюстрации.module.os');
    fs.writeFileSync(output,lines.join('\n'),'utf8');
    console.log('Generated '+path.relative(root,output));
  } finally {await browser.close();}
})().catch(e=>{console.error(e.message);process.exitCode=1;});
