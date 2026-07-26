package onec_forms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Минимальная фикстура Form.xml, покрывающая ключевые типы:
// AutoCommandBar + Button, Events, ChildItems с Pages/Page/UsualGroup/InputField/CheckBoxField,
// Attributes с MainAttribute и ValueTable+Columns, Commands, Parameters.
const minForm = `<?xml version="1.0" encoding="UTF-8"?>
<Form xmlns="http://v8.1c.ru/8.3/xcf/logform" xmlns:v8="http://v8.1c.ru/8.1/data/core" xmlns:cfg="http://v8.1c.ru/8.1/data/enterprise/current-config" xmlns:xr="http://v8.1c.ru/8.3/xcf/readable" xmlns:xs="http://www.w3.org/2001/XMLSchema" version="2.20">
  <AutoSaveDataInSettings>Use</AutoSaveDataInSettings>
  <VerticalScroll>useIfNecessary</VerticalScroll>
  <AutoCommandBar name="ФормаКоманднаяПанель" id="-1">
    <ChildItems>
      <Button name="КнПровести" id="100">
        <Type>CommandBarButton</Type>
        <Representation>PictureAndText</Representation>
        <CommandName>Form.Command.Провести</CommandName>
        <Title>
          <v8:item><v8:lang>ru</v8:lang><v8:content>Провести</v8:content></v8:item>
        </Title>
      </Button>
    </ChildItems>
  </AutoCommandBar>
  <Events>
    <Event name="OnOpen">ПриОткрытии</Event>
    <Event name="OnCreateAtServer">ПриСозданииНаСервере</Event>
  </Events>
  <ChildItems>
    <Pages name="Закладки" id="93">
      <Title>
        <v8:item><v8:lang>ru</v8:lang><v8:content>Закладки</v8:content></v8:item>
      </Title>
      <PagesRepresentation>TabsOnTop</PagesRepresentation>
      <ChildItems>
        <Page name="Настройки" id="94">
          <Title>
            <v8:item><v8:lang>ru</v8:lang><v8:content>Настройки</v8:content></v8:item>
          </Title>
          <ChildItems>
            <UsualGroup name="Шапка" id="200">
              <Title>
                <v8:item><v8:lang>ru</v8:lang><v8:content>Шапка</v8:content></v8:item>
              </Title>
              <Group>Vertical</Group>
              <ChildItems>
                <InputField name="ПолеНомер" id="201">
                  <DataPath>Объект.Номер</DataPath>
                  <Title>
                    <v8:item><v8:lang>ru</v8:lang><v8:content>Номер</v8:content></v8:item>
                  </Title>
                  <TitleLocation>Left</TitleLocation>
                  <Events>
                    <Event name="OnChange">НомерПриИзменении</Event>
                  </Events>
                </InputField>
                <CheckBoxField name="ПолеПроведен" id="202">
                  <DataPath>Объект.Проведен</DataPath>
                  <Title>
                    <v8:item><v8:lang>ru</v8:lang><v8:content>Проведен</v8:content></v8:item>
                  </Title>
                </CheckBoxField>
              </ChildItems>
            </UsualGroup>
          </ChildItems>
        </Page>
      </ChildItems>
    </Pages>
  </ChildItems>
  <Attributes>
    <Attribute name="Объект" id="1">
      <Type>
        <v8:Type>cfg:DocumentRef.РеализацияТоваров</v8:Type>
      </Type>
      <MainAttribute>true</MainAttribute>
      <Save>
        <Field>Объект.Номер</Field>
        <Field>Объект.Проведен</Field>
      </Save>
    </Attribute>
    <Attribute name="Товары" id="2">
      <Type>
        <v8:Type>v8:ValueTable</v8:Type>
      </Type>
      <Columns>
        <Column name="Номенклатура" id="3">
          <Type>
            <v8:Type>cfg:CatalogRef.Номенклатура</v8:Type>
          </Type>
        </Column>
        <Column name="Цена" id="4">
          <Type>
            <v8:Type>xs:decimal</v8:Type>
            <NumberQualifiers>
              <Digits>15</Digits>
              <FractionDigits>2</FractionDigits>
            </NumberQualifiers>
          </Type>
        </Column>
      </Columns>
    </Attribute>
  </Attributes>
  <Commands>
    <Command name="Провести" id="500">
      <Title>
        <v8:item><v8:lang>ru</v8:lang><v8:content>Провести</v8:content></v8:item>
      </Title>
      <Action>ПровестиКоманда</Action>
    </Command>
  </Commands>
  <Parameters/>
</Form>`

func writeFixture(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadFormXML_Minimal(t *testing.T) {
	path := writeFixture(t, "Form.xml", minForm)
	form, warns, err := ReadFormXML(path)
	if err != nil {
		t.Fatalf("ReadFormXML: %v", err)
	}
	if form == nil {
		t.Fatal("form is nil")
		return
	}
	if form.Version != "2.20" {
		t.Errorf("Version = %q", form.Version)
	}
	if !form.AutoSaveDataInSettings {
		t.Error("AutoSaveDataInSettings should be true")
	}
	if form.VerticalScroll != "useIfNecessary" {
		t.Errorf("VerticalScroll = %q", form.VerticalScroll)
	}

	// События формы
	if form.Events["OnOpen"] != "ПриОткрытии" {
		t.Errorf("Events[OnOpen] = %q", form.Events["OnOpen"])
	}
	if form.Events["OnCreateAtServer"] != "ПриСозданииНаСервере" {
		t.Errorf("Events[OnCreateAtServer] = %q", form.Events["OnCreateAtServer"])
	}

	// AutoCommandBar с одной кнопкой
	if form.AutoCommandBar == nil {
		t.Fatal("AutoCommandBar = nil")
	}
	if len(form.AutoCommandBar.Buttons) != 1 {
		t.Fatalf("buttons count = %d", len(form.AutoCommandBar.Buttons))
	}
	btn := form.AutoCommandBar.Buttons[0]
	if btn.Name != "КнПровести" || btn.OriginalID != "100" {
		t.Errorf("button = %+v", btn)
	}
	if btn.Title.Get("ru") != "Провести" {
		t.Errorf("button title = %q", btn.Title.Get("ru"))
	}
	if btn.CommandName != "Form.Command.Провести" {
		t.Errorf("button command = %q", btn.CommandName)
	}

	// Дерево ChildItems: 1 верхний Pages → 1 Page → 1 UsualGroup → 2 поля
	if len(form.Elements) != 1 {
		t.Fatalf("Elements top = %d", len(form.Elements))
	}
	pages := form.Elements[0]
	if pages.Kind != "Pages" || pages.Name != "Закладки" || pages.OriginalID != "93" {
		t.Errorf("pages = %+v", pages)
	}
	if pages.Title.Get("ru") != "Закладки" {
		t.Errorf("pages title = %q", pages.Title.Get("ru"))
	}
	if pages.Props["PagesRepresentation"] != "TabsOnTop" {
		t.Errorf("PagesRepresentation = %v", pages.Props["PagesRepresentation"])
	}

	if len(pages.Children) != 1 {
		t.Fatalf("pages.Children = %d", len(pages.Children))
	}
	page := pages.Children[0]
	if page.Kind != "Page" || page.Name != "Настройки" {
		t.Errorf("page = %+v", page)
	}

	if len(page.Children) != 1 {
		t.Fatalf("page.Children = %d", len(page.Children))
	}
	group := page.Children[0]
	if group.Kind != "UsualGroup" || group.Props["Group"] != "Vertical" {
		t.Errorf("group = %+v", group)
	}

	if len(group.Children) != 2 {
		t.Fatalf("group.Children = %d", len(group.Children))
	}
	input := group.Children[0]
	if input.Kind != "InputField" || input.Name != "ПолеНомер" || input.DataPath != "Объект.Номер" {
		t.Errorf("input = %+v", input)
	}
	if input.Events["OnChange"] != "НомерПриИзменении" {
		t.Errorf("input events = %+v", input.Events)
	}
	if input.Props["TitleLocation"] != "Left" {
		t.Errorf("TitleLocation = %v", input.Props["TitleLocation"])
	}

	check := group.Children[1]
	if check.Kind != "CheckBoxField" || check.DataPath != "Объект.Проведен" {
		t.Errorf("check = %+v", check)
	}

	// Реквизиты: 2 шт, второй — ValueTable с 2 колонками
	if len(form.Attributes) != 2 {
		t.Fatalf("Attributes count = %d", len(form.Attributes))
	}
	obj := form.Attributes[0]
	if obj.Name != "Объект" || !obj.MainAttribute || !obj.Save {
		t.Errorf("obj attr = %+v", obj)
	}
	if obj.TypeRef != "DocumentRef.РеализацияТоваров" {
		t.Errorf("obj.TypeRef = %q", obj.TypeRef)
	}

	tovary := form.Attributes[1]
	if tovary.Name != "Товары" || tovary.TypeRef != "ValueTable" {
		t.Errorf("tovary = %+v", tovary)
	}
	if len(tovary.Columns) != 2 {
		t.Fatalf("Tovary.Columns = %d", len(tovary.Columns))
	}
	if tovary.Columns[0].TypeRef != "CatalogRef.Номенклатура" {
		t.Errorf("col[0].TypeRef = %q", tovary.Columns[0].TypeRef)
	}
	if tovary.Columns[1].TypeRef != "decimal(15,2)" {
		t.Errorf("col[1].TypeRef = %q (warns: %v)", tovary.Columns[1].TypeRef, warns)
	}

	// Команды
	if len(form.Commands) != 1 || form.Commands[0].Action != "ПровестиКоманда" {
		t.Errorf("Commands = %+v", form.Commands)
	}

	// Не должно быть error-warnings на корректной форме
	for _, w := range warns {
		if w.Severity == SeverityError {
			t.Errorf("неожиданный error warning: %s", w)
		}
	}
}

func TestReadFormXML_RealFile(t *testing.T) {
	realPath := `C:\Projects\АА5БП3\УТ11УТ11\ПереносДанныхУТ11УТ11_52\Forms\Форма\Ext\Form.xml`
	if _, err := os.Stat(realPath); err != nil {
		t.Skip("real УТ11 Form.xml не найден, пропускаем")
	}
	form, warns, err := ReadFormXML(realPath)
	if err != nil {
		t.Fatalf("ReadFormXML: %v", err)
	}
	if form == nil {
		t.Fatal("form is nil")
		return
	}
	t.Logf("Version=%q AutoSave=%v VerticalScroll=%q", form.Version, form.AutoSaveDataInSettings, form.VerticalScroll)
	t.Logf("AutoCommandBar buttons: %d", len(form.AutoCommandBar.Buttons))
	t.Logf("Top elements: %d", len(form.Elements))
	t.Logf("Attributes: %d, Commands: %d, Parameters: %d", len(form.Attributes), len(form.Commands), len(form.Parameters))
	t.Logf("Events: %v", form.Events)
	t.Logf("Warnings: %d", len(warns))

	// Базовая адекватность: эти поля точно должны быть
	if form.Version != "2.20" {
		t.Errorf("Version = %q, want 2.20", form.Version)
	}
	if len(form.Attributes) == 0 {
		t.Error("real form must have attributes")
	}
	if form.AutoCommandBar == nil || len(form.AutoCommandBar.Buttons) == 0 {
		t.Error("real form must have command bar buttons")
	}

	// Глубина вложенности — реальная форма должна иметь >1 уровень
	maxDepth := 0
	var walk func(*IRElement, int)
	walk = func(el *IRElement, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		for _, c := range el.Children {
			walk(c, depth+1)
		}
	}
	for _, el := range form.Elements {
		walk(el, 1)
	}
	if maxDepth < 3 {
		t.Errorf("max depth = %d, want >= 3 (реальная форма должна быть глубокой)", maxDepth)
	}
	t.Logf("Max element depth: %d", maxDepth)

	// Все warnings должны быть info/warn, не error
	errCount := 0
	for _, w := range warns {
		if w.Severity == SeverityError {
			errCount++
			if errCount <= 5 {
				t.Logf("ERROR: %s", w)
			}
		}
	}
	if errCount > 0 {
		t.Errorf("реальная форма дала %d error-warnings", errCount)
	}

	// Один из реквизитов должен быть Объект с MainAttribute
	var mainAttr *IRAttribute
	for _, a := range form.Attributes {
		if a.MainAttribute {
			mainAttr = a
			break
		}
	}
	if mainAttr == nil {
		t.Error("ни один реквизит не помечен как MainAttribute")
	} else {
		t.Logf("MainAttribute: %s, type=%s, columns=%d", mainAttr.Name, mainAttr.TypeRef, len(mainAttr.Columns))
	}

	// УТ11 содержит несколько ValueTable-реквизитов
	var vtCount int
	for _, a := range form.Attributes {
		if strings.HasPrefix(a.TypeRef, "ValueTable") || a.TypeRef == "ValueTable" {
			vtCount++
		}
	}
	t.Logf("ValueTable attributes: %d", vtCount)
}
